package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/realtime"
	"minicloud/internal/repo"
)

// Handler exposes the developer console API: manage games, inspect players,
// author dialogue scripts. Auth is a single operator account from config.
type Handler struct {
	Issuer    *auth.TokenIssuer
	User      string
	Pass      string
	Games        repo.Games
	Players      repo.Players
	Accounts     repo.Accounts
	Data         repo.PlayerData
	Dialogues    repo.Dialogues
	OAuthClients repo.OAuthClients
	Hub          *realtime.Hub

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
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(h.User)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.Pass)) == 1
	if !userOK || !passOK {
		httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
		return
	}
	token, err := h.Issuer.IssueAdmin(req.Username)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"token": token})
}

// Me handles GET /admin/api/me: who the console is signed in as, when the
// session ends, and whether the deployment is still on default credentials —
// that last one otherwise only ever appears in the startup log.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	res := map[string]any{
		"username": claims.Subject,
		"role":     claims.Role,
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
	game, err := h.Games.Create(r.Context(), req.Name)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, game)
}

// ListGames handles GET /admin/api/games, with online player counts.
func (h *Handler) ListGames(w http.ResponseWriter, r *http.Request) {
	games, err := h.Games.List(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	type gameWithStats struct {
		repo.Game
		Online int `json:"online"`
	}
	out := make([]gameWithStats, len(games))
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
	accounts, err := h.Accounts.List(r.Context(), q.Get("search"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

// DeleteAccount handles DELETE /admin/api/accounts/{id}. Game players survive
// and simply lose their link to the passport.
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	err := h.Accounts.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no account with this id")
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

// ListOAuthClients handles GET /admin/api/oauth/clients.
func (h *Handler) ListOAuthClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.OAuthClients.List(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"clients": clients})
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
	client, err := h.OAuthClients.Create(r.Context(), req.Name, req.RedirectURLs, req.Confidential)
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
	err := h.OAuthClients.Update(r.Context(), r.PathValue("client_id"), req.Name, req.RedirectURLs)
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
	err := h.OAuthClients.Delete(r.Context(), r.PathValue("client_id"))
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
