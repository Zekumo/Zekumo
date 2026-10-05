package hooks

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zekumo/internal/httpx"
	"zekumo/internal/repo"
	"zekumo/internal/tenant"
)

// KnownEvents is documentation for the console; hooks may subscribe to any
// subset, or * for everything.
var KnownEvents = []string{
	"player.registered", "player.login", "player.banned", "player.unbanned",
	"leaderboard.score", "achievement.unlocked", "release.published",
}

type Handler struct {
	Bus *Bus
}

func validHookURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// List handles GET /admin/api/games/{id}/webhooks.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	hooks, err := h.Bus.Webhooks.ListByGame(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if scope := tenant.FromContext(r.Context()); scope != nil {
		redactWebhookSecrets(hooks, scope.Role)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"webhooks": hooks, "known_events": KnownEvents})
}

func redactWebhookSecrets(hooks []repo.Webhook, role string) {
	if role != "viewer" {
		return
	}
	for i := range hooks {
		hooks[i].Secret = ""
	}
}

// Create handles POST /admin/api/games/{id}/webhooks with {url, events}.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		Events string `json:"events"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if !validHookURL(req.URL) {
		httpx.Error(w, http.StatusBadRequest, "bad_url", "url must be http(s)")
		return
	}
	if strings.TrimSpace(req.Events) == "" {
		req.Events = "*"
	}
	hook, err := h.Bus.Webhooks.Create(r.Context(), r.PathValue("id"), req.URL, req.Events)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, hook)
}

// Update handles PUT /admin/api/webhooks/{wid} with {url, events, enabled}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string `json:"url"`
		Events  string `json:"events"`
		Enabled *bool  `json:"enabled"` // pointer: omitting it must not disable the hook
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if !validHookURL(req.URL) {
		httpx.Error(w, http.StatusBadRequest, "bad_url", "url must be http(s)")
		return
	}
	if strings.TrimSpace(req.Events) == "" {
		req.Events = "*"
	}
	enabled := req.Enabled == nil || *req.Enabled
	err := h.Bus.Webhooks.Update(r.Context(), r.PathValue("wid"), req.URL, req.Events, enabled)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no webhook with this id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// Delete handles DELETE /admin/api/webhooks/{wid}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	err := h.Bus.Webhooks.Delete(r.Context(), r.PathValue("wid"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no webhook with this id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// Deliveries handles GET /admin/api/webhooks/{wid}/deliveries.
func (h *Handler) Deliveries(w http.ResponseWriter, r *http.Request) {
	deliveries, err := h.Bus.Deliveries.Recent(r.Context(), r.PathValue("wid"), 50)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

// Test handles POST /admin/api/webhooks/{wid}/test — one synchronous POST of a
// webhook.test event, returning the upstream status code.
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	hook, err := h.Bus.Webhooks.ByID(r.Context(), r.PathValue("wid"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no webhook with this id")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"event":     "webhook.test",
		"game_id":   hook.GameID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      map[string]string{"hello": "zekumo"},
	})
	status := h.Bus.post(*hook, job{gameID: hook.GameID, event: "webhook.test", payload: payload})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": status, "ok": status >= 200 && status < 300})
}
