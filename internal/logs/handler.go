package logs

import (
	"net/http"
	"strconv"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/tenant"
)

type Handler struct{ Svc *Service }

// AdminQuery handles GET /admin/api/logs?game_id=&level=&source=&q=&before_id=&limit=.
func (h *Handler) AdminQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	beforeID, _ := strconv.ParseInt(q.Get("before_id"), 10, 64)
	entries, err := h.Svc.Query(r.Context(), QueryFilter{
		WorkspaceID: tenant.FromContext(r.Context()).WorkspaceID,
		GameID:      q.Get("game_id"),
		Level:       q.Get("level"),
		Source:      q.Get("source"),
		Search:      q.Get("q"),
		BeforeID:    beforeID,
		Limit:       limit,
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

var clientLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// ClientReport handles POST /v1/logs (player token) — game clients report
// errors and diagnostics into the same pipeline.
func (h *Handler) ClientReport(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		Level   string         `json:"level"`
		Event   string         `json:"event"`
		Message string         `json:"message"`
		Fields  map[string]any `json:"fields"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if !clientLevels[req.Level] {
		req.Level = "info"
	}
	if req.Message == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_message", "message is required")
		return
	}
	if req.Fields == nil {
		req.Fields = map[string]any{}
	}
	req.Fields["player_id"] = claims.Subject
	h.Svc.Write(claims.GameID, req.Level, "client", req.Event, req.Message, req.Fields)
	httpx.JSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}
