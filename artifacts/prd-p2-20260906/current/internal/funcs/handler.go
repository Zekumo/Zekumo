package funcs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

var fnName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type Handler struct {
	Functions repo.Functions
	Games     repo.Games
	Players   repo.Players
	Runtime   *Runtime
	Logs      LogSink
}

// LogSink receives structured log entries; nil disables logging.
type LogSink interface {
	Write(gameID, level, source, event, message string, fields map[string]any)
}

// errBodyTooLarge is reported to the caller instead of running the function
// with a silently empty body.
var errBodyTooLarge = errors.New("request body exceeds 1MB")

func buildRequest(r *http.Request, player any) (Request, error) {
	var body any
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return Request{}, errBodyTooLarge
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return Request{}, errors.New("request body is not valid JSON")
		}
	}
	query := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}
	return Request{Method: r.Method, Body: body, Query: query, Player: player}, nil
}

// request builds the script input, answering the caller directly on bad input.
func request(w http.ResponseWriter, r *http.Request, player any) (Request, bool) {
	req, err := buildRequest(r, player)
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "bad_body", err.Error())
		return Request{}, false
	}
	return req, true
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request, gameID string, fn *repo.CloudFunction, req Request) {
	res, err := h.Runtime.Execute(r.Context(), gameID, fn, req)
	if errors.Is(err, ErrBusy) {
		w.Header().Set("Retry-After", "1")
		httpx.Error(w, http.StatusServiceUnavailable, "busy", err.Error())
		return
	}
	if err != nil {
		if h.Logs != nil {
			h.Logs.Write(gameID, "error", "funcs", fn.Name, err.Error(), nil)
		}
		httpx.Error(w, http.StatusBadRequest, "function_error", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// Call handles GET/POST /v1/functions/{name} with a player token.
func (h *Handler) Call(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	fn, err := h.Functions.Get(r.Context(), claims.GameID, r.PathValue("name"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no cloud function with this name")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if !fn.Enabled {
		httpx.Error(w, http.StatusForbidden, "disabled", "this function is disabled")
		return
	}
	player := map[string]any{"id": claims.Subject, "nickname": claims.Nickname}
	req, ok := request(w, r, player)
	if !ok {
		return
	}
	h.run(w, r, claims.GameID, fn, req)
}

// PublicCall handles GET/POST /v1/apps/{app_id}/functions/{name} without a
// token; only functions marked public are reachable this way.
func (h *Handler) PublicCall(w http.ResponseWriter, r *http.Request) {
	game, err := h.Games.ByAppID(r.Context(), r.PathValue("app_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_app", "no game with this app_id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	fn, err := h.Functions.Get(r.Context(), game.ID, r.PathValue("name"))
	if errors.Is(err, repo.ErrNotFound) || (err == nil && (!fn.Enabled || !fn.Public)) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no public cloud function with this name")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	req, ok := request(w, r, nil)
	if !ok {
		return
	}
	h.run(w, r, game.ID, fn, req)
}

// --- admin ---

// List handles GET /admin/api/games/{id}/functions.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	fns, err := h.Functions.List(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"functions": fns})
}

// Get handles GET /admin/api/games/{id}/functions/{name} (with code).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	fn, err := h.Functions.Get(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no cloud function with this name")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, fn)
}

// Put handles PUT /admin/api/games/{id}/functions/{name}.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !fnName.MatchString(name) {
		httpx.Error(w, http.StatusBadRequest, "bad_name", "function name must match [a-zA-Z0-9_-]{1,64}")
		return
	}
	var req struct {
		Code     string `json:"code"`
		Enabled  bool   `json:"enabled"`
		Public   bool   `json:"public"`
		CronSecs int    `json:"cron_secs"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if len(req.Code) > 256<<10 {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "code_too_large", "code exceeds 256KB")
		return
	}
	if req.CronSecs < 0 || (req.CronSecs > 0 && req.CronSecs < 10) {
		httpx.Error(w, http.StatusBadRequest, "bad_cron", "cron_secs must be 0 (off) or >= 10")
		return
	}
	fn, err := h.Functions.Upsert(r.Context(), r.PathValue("id"), name, req.Code, req.Enabled, req.Public, req.CronSecs)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, fn)
}

// Delete handles DELETE /admin/api/games/{id}/functions/{name}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	err := h.Functions.Delete(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no cloud function with this name")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// Test handles POST /admin/api/games/{id}/functions/{name}/test — runs the
// saved code regardless of enabled/public, with the posted body as request.body.
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	fn, err := h.Functions.Get(r.Context(), gameID, r.PathValue("name"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no cloud function with this name")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	req, ok := request(w, r, nil)
	if !ok {
		return
	}
	h.run(w, r, gameID, fn, req)
}
