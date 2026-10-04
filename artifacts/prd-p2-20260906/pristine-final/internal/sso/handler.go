package sso

import (
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

// Handler serves the platform-account (通行证) API: register/login, ticket
// issuance for games, the authorize-page info endpoint, and player binding.
type Handler struct {
	Issuer   *auth.TokenIssuer
	Accounts repo.Accounts
	Players  repo.Players
	Games    repo.Games
	Tickets  Tickets
	Attempts auth.AttemptLimiter // optional per-account brute-force guard
}

type accountResponse struct {
	Token   string        `json:"token"`
	Account *repo.Account `json:"account"`
}

// Register handles POST /sso/api/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Nickname string `json:"nickname"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 {
		httpx.Error(w, http.StatusBadRequest, "bad_username", "username must be at least 3 characters")
		return
	}
	if len(req.Password) < 6 {
		httpx.Error(w, http.StatusBadRequest, "bad_password", "password must be at least 6 characters")
		return
	}
	if _, err := h.Accounts.ByUsername(r.Context(), req.Username); err == nil {
		httpx.Error(w, http.StatusConflict, "user_exists", "username already taken")
		return
	} else if !errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	nickname := req.Nickname
	if nickname == "" {
		nickname = req.Username
	}
	account, err := h.Accounts.Create(r.Context(), req.Username, string(hash), nickname)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	h.respondWithToken(w, account)
}

// Login handles POST /sso/api/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	identity := "account:" + strings.ToLower(strings.TrimSpace(req.Username))
	if h.Attempts != nil && h.Attempts.Locked(r.Context(), identity) {
		httpx.Error(w, http.StatusTooManyRequests, "locked",
			"too many failed attempts for this account, try again later")
		return
	}
	badCredentials := func() {
		if h.Attempts != nil && h.Attempts.Fail(r.Context(), identity) {
			httpx.Error(w, http.StatusTooManyRequests, "locked",
				"too many failed attempts for this account, try again later")
			return
		}
		httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
	}

	account, err := h.Accounts.ByUsername(r.Context(), strings.TrimSpace(req.Username))
	if errors.Is(err, repo.ErrNotFound) {
		auth.BurnPasswordCheck(req.Password)
		badCredentials()
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(req.Password)) != nil {
		badCredentials()
		return
	}
	if h.Attempts != nil {
		h.Attempts.Succeed(r.Context(), identity)
	}
	_ = h.Accounts.TouchLogin(r.Context(), account.ID)
	h.respondWithToken(w, account)
}

func (h *Handler) respondWithToken(w http.ResponseWriter, account *repo.Account) {
	token, err := h.Issuer.IssueAccount(account.ID, account.Nickname)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, accountResponse{Token: token, Account: account})
}

// Me handles GET /sso/api/me (account token) — the account plus every game
// player linked to it.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	account, err := h.Accounts.ByID(r.Context(), claims.Subject)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	linked, err := h.Players.ListByAccount(r.Context(), account.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"account": account, "players": linked})
}

// IssueTicket handles POST /sso/api/tickets (account token) with {app_id}.
// The ticket is one-time, expires in 5 minutes, and only works for that game.
func (h *Handler) IssueTicket(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		AppID string `json:"app_id"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	game, err := h.Games.ByAppID(r.Context(), req.AppID)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_app", "no game with this app_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if game.Status != "active" {
		httpx.Error(w, http.StatusForbidden, "game_disabled", "this game is disabled")
		return
	}
	ticket, err := h.Tickets.Issue(r.Context(), claims.Subject, game.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"expires_in": int(ticketTTL.Seconds()),
	})
}

// AuthorizeInfo handles GET /sso/api/authorize/info?app_id=&redirect_uri=.
// The hosted authorize page calls it before showing the login form; it refuses
// redirect targets that are not on the game's whitelist.
func (h *Handler) AuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	game, err := h.Games.ByAppID(r.Context(), q.Get("app_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_app", "no game with this app_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"game_name":      game.Name,
		"redirect_valid": httpx.RedirectAllowed(game.SSORedirectURLs, q.Get("redirect_uri")),
	})
}

// BindPlayer handles POST /v1/player/bind (player token). The body carries
// either a ticket for this game or the account's username/password. On
// success the current player is linked to the account, so later SSO logins
// resume this player (save data survives device changes).
func (h *Handler) BindPlayer(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var creds auth.Credentials
	if httpx.Decode(w, r, &creds) != nil {
		return
	}
	provider := Provider{Accounts: h.Accounts, Players: h.Players, Tickets: h.Tickets}
	accountID, err := provider.resolveAccount(r.Context(), claims.GameID, creds)
	if errors.Is(err, auth.ErrBadCredentials) {
		httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bind_failed", err.Error())
		return
	}
	switch err := h.Players.Bind(r.Context(), claims.Subject, accountID); {
	case errors.Is(err, repo.ErrPlayerAlreadyBound):
		httpx.Error(w, http.StatusConflict, "player_already_bound", "this player is already bound to an account")
	case errors.Is(err, repo.ErrAccountAlreadyBound):
		httpx.Error(w, http.StatusConflict, "account_already_bound", "this account already has a player in this game")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"bound": true, "account_id": accountID})
	}
}
