// Package oauth implements an OAuth2 authorization-code provider on top of
// platform accounts: PKCE for public clients, rotating refresh tokens with
// reuse detection, and a hosted consent page.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

const (
	codeTTL         = 5 * time.Minute
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
	defaultScope    = "profile"
)

type Handler struct {
	Issuer        *auth.TokenIssuer
	RDB           *redis.Client
	Clients       repo.OAuthClients
	Grants        repo.OAuthGrants
	RefreshTokens repo.OAuthRefreshTokens
	Accounts      repo.Accounts
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// knownScopes gates what a client may ask for, so a client cannot mint an
// arbitrary scope string that some future endpoint might trust.
var knownScopes = map[string]bool{"profile": true}

func normalizeScope(scope string) (string, error) {
	if strings.TrimSpace(scope) == "" {
		return defaultScope, nil
	}
	parts := strings.Fields(scope)
	for _, p := range parts {
		if !knownScopes[p] {
			return "", fmt.Errorf("unknown scope %q", p)
		}
	}
	return strings.Join(parts, " "), nil
}

// codePayload is what an authorization code stands for while it lives in Redis.
type codePayload struct {
	AccountID   string `json:"account_id"`
	ClientPK    string `json:"client_pk"`
	ClientID    string `json:"client_id"`
	RedirectURI string `json:"redirect_uri"`
	Scope       string `json:"scope"`
	Challenge   string `json:"challenge,omitempty"`
	Method      string `json:"method,omitempty"`
}

// Info handles GET /oauth/api/info?client_id=&redirect_uri= for the consent page.
func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, err := h.Clients.ByClientID(r.Context(), q.Get("client_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_client", "no application with this client_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"client_name":    client.Name,
		"redirect_valid": httpx.RedirectAllowed(client.RedirectURLs, q.Get("redirect_uri")),
	})
}

// Approve handles POST /oauth/api/approve (account token): the user consented,
// mint a one-time authorization code and record the grant.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		ClientID            string `json:"client_id"`
		RedirectURI         string `json:"redirect_uri"`
		Scope               string `json:"scope"`
		CodeChallenge       string `json:"code_challenge"`
		CodeChallengeMethod string `json:"code_challenge_method"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	client, err := h.Clients.ByClientID(r.Context(), req.ClientID)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_client", "no application with this client_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !httpx.RedirectAllowed(client.RedirectURLs, req.RedirectURI) {
		httpx.Error(w, http.StatusBadRequest, "bad_redirect", "redirect_uri is not whitelisted for this application")
		return
	}
	if client.ClientSecret == "" && req.CodeChallenge == "" {
		httpx.Error(w, http.StatusBadRequest, "pkce_required", "public clients must use PKCE (code_challenge)")
		return
	}
	// "plain" is not accepted: the verifier travels in the front channel, so
	// it protects nothing an attacker who can see the code cannot also see.
	if req.CodeChallengeMethod != "" && req.CodeChallengeMethod != "S256" {
		httpx.Error(w, http.StatusBadRequest, "bad_challenge_method", "code_challenge_method must be S256")
		return
	}
	scope, err := normalizeScope(req.Scope)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_scope", err.Error())
		return
	}
	if err := h.Grants.Upsert(r.Context(), claims.Subject, client.ID, scope); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	code := "ac_" + randomHex(24)
	payload, _ := json.Marshal(codePayload{
		AccountID:   claims.Subject,
		ClientPK:    client.ID,
		ClientID:    client.ClientID,
		RedirectURI: req.RedirectURI,
		Scope:       scope,
		Challenge:   req.CodeChallenge,
		Method:      req.CodeChallengeMethod,
	})
	if err := h.RDB.Set(r.Context(), "oauth:code:"+code, payload, codeTTL).Err(); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"code": code})
}

// tokenError responds in RFC 6749 error format (this endpoint is spoken to by
// third-party OAuth libraries, not our own clients).
func tokenError(w http.ResponseWriter, status int, code, desc string) {
	httpx.JSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// Token handles POST /oauth/token (form-encoded per RFC, JSON also accepted).
func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	params, err := tokenParams(r)
	if err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	client, err := h.authClient(r.Context(), params)
	if err != nil {
		tokenError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	switch params["grant_type"] {
	case "authorization_code":
		h.exchangeCode(w, r, client, params)
	case "refresh_token":
		h.refresh(w, r, client, params)
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
	}
}

func tokenParams(r *http.Request) (map[string]string, error) {
	params := map[string]string{}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&params); err != nil {
			return nil, errors.New("invalid JSON body")
		}
		return params, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, errors.New("invalid form body")
	}
	for k, v := range r.PostForm {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	return params, nil
}

func (h *Handler) authClient(ctx context.Context, params map[string]string) (*repo.OAuthClient, error) {
	client, err := h.Clients.ByClientID(ctx, params["client_id"])
	if errors.Is(err, repo.ErrNotFound) {
		return nil, errors.New("unknown client_id")
	}
	if err != nil {
		return nil, err
	}
	if client.ClientSecret != "" { // confidential clients must authenticate
		if subtle.ConstantTimeCompare([]byte(client.ClientSecret), []byte(params["client_secret"])) != 1 {
			return nil, errors.New("wrong client_secret")
		}
	}
	return client, nil
}

func (h *Handler) exchangeCode(w http.ResponseWriter, r *http.Request, client *repo.OAuthClient, params map[string]string) {
	raw, err := h.RDB.GetDel(r.Context(), "oauth:code:"+params["code"]).Result()
	if errors.Is(err, redis.Nil) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired or already used")
		return
	}
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	var code codePayload
	if json.Unmarshal([]byte(raw), &code) != nil || code.ClientID != client.ClientID {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "authorization code was issued to a different client")
		return
	}
	if code.RedirectURI != params["redirect_uri"] {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	if code.Challenge != "" && !verifyPKCE(code.Challenge, code.Method, params["code_verifier"]) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	h.issueTokens(w, r, client, code.AccountID, code.Scope, "" /* new family */)
}

func verifyPKCE(challenge, method, verifier string) bool {
	if verifier == "" || (method != "" && method != "S256") {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request, client *repo.OAuthClient, params map[string]string) {
	row, err := h.RefreshTokens.ByHash(r.Context(), hashToken(params["refresh_token"]))
	if errors.Is(err, repo.ErrNotFound) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or revoked")
		return
	}
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if row.ClientPK != client.ID {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token belongs to a different client")
		return
	}
	if time.Now().After(row.ExpiresAt) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token has expired")
		return
	}
	fresh, err := h.RefreshTokens.MarkUsed(r.Context(), row.ID)
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if !fresh { // reuse of a rotated token: assume theft, kill the whole chain
		_ = h.RefreshTokens.RevokeFamily(r.Context(), row.Family)
		tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected; all sessions for this app were revoked")
		return
	}
	h.issueTokens(w, r, client, row.AccountID, row.Scope, row.Family)
}

// issueTokens mints an access token plus a rotated refresh token. An empty
// family starts a new rotation chain.
func (h *Handler) issueTokens(w http.ResponseWriter, r *http.Request, client *repo.OAuthClient, accountID, scope, family string) {
	access, err := h.Issuer.IssueOAuth(accountID, client.ClientID, scope, accessTokenTTL)
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if family == "" {
		family = newFamilyID()
	}
	refresh := "rt_" + randomHex(32)
	err = h.RefreshTokens.Insert(r.Context(), hashToken(refresh), family, accountID, client.ID, scope,
		time.Now().Add(refreshTokenTTL))
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTokenTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

// newFamilyID returns a random UUIDv4 to label a refresh-token rotation chain.
func newFamilyID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

// Userinfo handles GET /oauth/userinfo with an OAuth access token.
func (h *Handler) Userinfo(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	// Access tokens are stateless and live an hour, so revocation would not
	// bite until they expire; re-check the grant on every use instead.
	granted, err := h.Grants.Exists(r.Context(), claims.Subject, claims.Client)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !granted {
		httpx.Error(w, http.StatusUnauthorized, "revoked", "this authorization has been revoked")
		return
	}
	account, err := h.Accounts.ByID(r.Context(), claims.Subject)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"sub":      account.ID,
		"username": account.Username,
		"nickname": account.Nickname,
		"scope":    claims.Scope,
	})
}

// Authorizations handles GET /sso/api/authorizations (account token).
func (h *Handler) Authorizations(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	grants, err := h.Grants.ListByAccount(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"authorizations": grants})
}

// RevokeAuthorization handles DELETE /sso/api/authorizations/{client_id}.
func (h *Handler) RevokeAuthorization(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	client, err := h.Clients.ByClientID(r.Context(), r.PathValue("client_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_client", "no application with this client_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	err = h.Grants.Revoke(r.Context(), claims.Subject, client.ID)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "you have not authorized this application")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}
