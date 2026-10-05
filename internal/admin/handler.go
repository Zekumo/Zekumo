package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/realtime"
	"zekumo/internal/repo"
	"zekumo/internal/tenant"
)

// Handler exposes the developer console API: manage games, inspect players,
// author dialogue scripts. Auth is a single operator account from config.
type Handler struct {
	Issuer       *auth.TokenIssuer
	User         string
	Pass         string
	Games        repo.Games
	Players      repo.Players
	Accounts     repo.Accounts
	Data         repo.PlayerData
	Dialogues    repo.Dialogues
	OAuthClients repo.OAuthClients
	Hub          *realtime.Hub
	Tenants      tenant.Store

	// DefaultJWTSecret reports that tokens are signed with the shipped
	// development secret, so the console can say so out loud.
	DefaultJWTSecret bool
}

// defaultAdminPass is the password shipped in the docs and compose file; a
// deployment still using it should be told.
const defaultAdminPass = "admin123"

// Login handles POST /admin/api/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(h.User)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.Pass)) == 1
	var identity *tenant.Identity
	var err error
	if userOK && passOK {
		identity, err = h.Tenants.EnsureBootstrap(r.Context(), req.Username)
	} else {
		account, accountErr := h.Accounts.ByUsername(r.Context(), req.Username)
		if errors.Is(accountErr, repo.ErrNotFound) {
			auth.BurnPasswordCheck(req.Password)
			httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
			return
		}
		if accountErr != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", accountErr.Error())
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(req.Password)) != nil {
			httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
			return
		}
		identity, err = h.Tenants.IdentityByAccount(r.Context(), account.ID)
		if errors.Is(err, tenant.ErrNotFound) {
			httpx.Error(w, http.StatusForbidden, "no_membership", "this account has no console membership")
			return
		}
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	workspaces, err := h.Tenants.Workspaces(r.Context(), identity.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if len(workspaces) == 0 {
		httpx.Error(w, http.StatusForbidden, "no_membership", "this account has no console membership")
		return
	}
	token, err := h.Issuer.IssueAdmin(identity.ID, identity.Username)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token": token, "user": identity, "workspaces": workspaces,
		"default_workspace_id": workspaces[0].ID,
	})
}

// Me handles GET /admin/api/me: who the console is signed in as, when the
// session ends, and whether the deployment is still on default credentials —
// that last one otherwise only ever appears in the startup log.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	identity, err := h.Tenants.IdentityByID(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "invalid_token", "console identity no longer exists")
		return
	}
	workspaces, err := h.Tenants.Workspaces(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	res := map[string]any{
		"username":   identity.Username,
		"user":       identity,
		"role":       claims.Role,
		"workspaces": workspaces,
	}
	if selected := r.Header.Get(tenant.WorkspaceHeader); selected != "" {
		for _, workspace := range workspaces {
			if workspace.ID == selected {
				res["active_workspace_id"] = selected
				res["active_role"] = workspace.Role
				break
			}
		}
	}
	if claims.IssuedAt != nil {
		res["issued_at"] = claims.IssuedAt.Time
	}
	if claims.ExpiresAt != nil {
		res["expires_at"] = claims.ExpiresAt.Time
	}
	warnings := []string{}
	if h.Pass == defaultAdminPass {
		warnings = append(warnings, "default_password")
	}
	if h.DefaultJWTSecret {
		warnings = append(warnings, "default_jwt_secret")
	}
	res["warnings"] = warnings
	httpx.JSON(w, http.StatusOK, res)
}

// CreateGame handles POST /admin/api/games with {name}.
func (h *Handler) CreateGame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "name is required")
		return
	}
	scope := tenant.FromContext(r.Context())
	game, err := h.Games.CreateForWorkspace(r.Context(), scope.WorkspaceID, req.Name)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, game)
}

// ListGames handles GET /admin/api/games, with online player counts.
func (h *Handler) ListGames(w http.ResponseWriter, r *http.Request) {
	scope := tenant.FromContext(r.Context())
	games, err := h.Games.ListByWorkspace(r.Context(), scope.WorkspaceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	type gameWithStats struct {
		repo.Game
		Online int `json:"online"`
	}
	out := make([]gameWithStats, len(games))
	redactGameSecrets(games, scope.Role)
	for i, g := range games {
		out[i] = gameWithStats{Game: g, Online: h.Hub.OnlineCount(g.ID)}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"games": out})
}

// DeleteGame handles DELETE /admin/api/games/{id} — cascades to all game data.
func (h *Handler) DeleteGame(w http.ResponseWriter, r *http.Request) {
	err := h.Games.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no game with this id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ListPlayers handles GET /admin/api/games/{id}/players?search=&limit=&offset=.
func (h *Handler) ListPlayers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	players, err := h.Players.ListByGame(r.Context(), r.PathValue("id"), q.Get("search"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"players": players})
}

// PlayerData handles GET /admin/api/players/{pid}/data — full save inspection.
func (h *Handler) PlayerData(w http.ResponseWriter, r *http.Request) {
	entries, err := h.Data.ListWithValues(r.Context(), r.PathValue("pid"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// ListAccounts handles GET /admin/api/accounts?search=&limit=&offset= —
// platform-wide SSO accounts (通行证).
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	accounts, err := h.Accounts.ListByWorkspace(r.Context(), tenant.FromContext(r.Context()).WorkspaceID, q.Get("search"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

// DeleteAccount handles DELETE /admin/api/accounts/{id}. Game players survive
// and simply lose their link to the passport.
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	err := h.Accounts.DeleteForWorkspace(r.Context(), tenant.FromContext(r.Context()).WorkspaceID, r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no account with this id")
		return
	}
	if errors.Is(err, repo.ErrAccountSharedAcrossWorkspaces) {
		httpx.Error(w, http.StatusConflict, "account_shared", "account is linked to another workspace and cannot be deleted here")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// UpdateGameSSO handles PUT /admin/api/games/{id}/sso with
// {"redirect_urls": "https://a.example.com/cb\nhttps://b.example.com/cb"}.
func (h *Handler) UpdateGameSSO(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURLs string `json:"redirect_urls"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	err := h.Games.UpdateSSORedirectURLs(r.Context(), r.PathValue("id"), req.RedirectURLs)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no game with this id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// UpdateGameHTTPAllowlist handles PUT /admin/api/games/{id}/http-allowlist
// with {"hosts": "api.example.com\n*.trusted.com"}.
func (h *Handler) UpdateGameHTTPAllowlist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hosts string `json:"hosts"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	err := h.Games.UpdateFuncHTTPAllowlist(r.Context(), r.PathValue("id"), req.Hosts)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no game with this id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// UpdateGameFriendLimit handles PUT /admin/api/games/{id}/friend-limit.
func (h *Handler) UpdateGameFriendLimit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Limit int `json:"limit"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Limit < 1 || req.Limit > 10000 {
		httpx.Error(w, http.StatusBadRequest, "bad_limit", "limit must be between 1 and 10000")
		return
	}
	if err := h.Games.UpdateFriendLimit(r.Context(), r.PathValue("id"), req.Limit); errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no game with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// ListOAuthClients handles GET /admin/api/oauth/clients.
func (h *Handler) ListOAuthClients(w http.ResponseWriter, r *http.Request) {
	scope := tenant.FromContext(r.Context())
	clients, err := h.OAuthClients.ListByWorkspace(r.Context(), scope.WorkspaceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	redactOAuthSecrets(clients, scope.Role)
	httpx.JSON(w, http.StatusOK, map[string]any{"clients": clients})
}

func redactGameSecrets(games []repo.Game, role string) {
	if role != "viewer" {
		return
	}
	for i := range games {
		games[i].AppSecret = ""
	}
}

func redactOAuthSecrets(clients []repo.OAuthClient, role string) {
	if role != "viewer" {
		return
	}
	for i := range clients {
		clients[i].ClientSecret = ""
	}
}

// CreateOAuthClient handles POST /admin/api/oauth/clients with
// {"name", "redirect_urls", "confidential"}.
func (h *Handler) CreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		RedirectURLs string `json:"redirect_urls"`
		Confidential bool   `json:"confidential"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "name is required")
		return
	}
	client, err := h.OAuthClients.CreateForWorkspace(r.Context(), tenant.FromContext(r.Context()).WorkspaceID, req.Name, req.RedirectURLs, req.Confidential)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, client)
}

// UpdateOAuthClient handles PUT /admin/api/oauth/clients/{client_id}.
func (h *Handler) UpdateOAuthClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		RedirectURLs string `json:"redirect_urls"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	err := h.OAuthClients.UpdateForWorkspace(r.Context(), tenant.FromContext(r.Context()).WorkspaceID, r.PathValue("client_id"), req.Name, req.RedirectURLs)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no application with this client_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// DeleteOAuthClient handles DELETE /admin/api/oauth/clients/{client_id}.
func (h *Handler) DeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	err := h.OAuthClients.DeleteForWorkspace(r.Context(), tenant.FromContext(r.Context()).WorkspaceID, r.PathValue("client_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no application with this client_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// ListDialogues handles GET /admin/api/games/{id}/dialogues.
func (h *Handler) ListDialogues(w http.ResponseWriter, r *http.Request) {
	scripts, err := h.Dialogues.List(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scripts": scripts})
}

// GetDialogue handles GET /admin/api/games/{id}/dialogues/{key}.
func (h *Handler) GetDialogue(w http.ResponseWriter, r *http.Request) {
	script, err := h.Dialogues.Get(r.Context(), r.PathValue("id"), r.PathValue("key"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no dialogue script with this key")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, script)
}

// PutDialogue handles PUT /admin/api/games/{id}/dialogues/{key} with {title, content}.
func (h *Handler) PutDialogue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title   string          `json:"title"`
		Content json.RawMessage `json:"content"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if len(req.Content) == 0 {
		httpx.Error(w, http.StatusBadRequest, "missing_content", "content is required")
		return
	}
	script, err := h.Dialogues.Upsert(r.Context(), r.PathValue("id"), r.PathValue("key"), req.Title, req.Content)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, script)
}

// DeleteDialogue handles DELETE /admin/api/games/{id}/dialogues/{key}.
func (h *Handler) DeleteDialogue(w http.ResponseWriter, r *http.Request) {
	err := h.Dialogues.Delete(r.Context(), r.PathValue("id"), r.PathValue("key"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no dialogue script with this key")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
