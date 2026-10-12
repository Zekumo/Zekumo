// Package matches exposes durable server-authored match records. Player APIs are read-only.
package matches

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

type Handler struct{ Records repo.Matches }

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		httpx.Error(w, 404, "not_found", "match or same-game participant not found")
	case errors.Is(err, repo.ErrMatchConflict):
		httpx.Error(w, 409, "match_conflict", err.Error())
	case errors.Is(err, repo.ErrInvalidMatch):
		httpx.Error(w, 400, "invalid_match", err.Error())
	default:
		httpx.Error(w, 500, "internal", "match storage failed")
	}
}
func matchID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("match_id"), 10, 64)
	if err != nil || id < 1 {
		httpx.Error(w, 400, "invalid_match_id", "positive match ID required")
		return 0, false
	}
	return id, true
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request, gameID, playerID string) {
	before := int64(0)
	limit := 20
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			httpx.Error(w, 400, "bad_cursor", "before_id must be positive")
			return
		}
		before = n
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			httpx.Error(w, 400, "bad_limit", "limit must be 1..50")
			return
		}
		limit = n
	}
	items, err := h.Records.List(r.Context(), gameID, playerID, before, limit)
	if err != nil {
		respondError(w, err)
		return
	}
	next := ""
	if len(items) == limit {
		next = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	httpx.JSON(w, 200, map[string]any{"matches": items, "next_before_id": next})
}
func (h *Handler) PlayerList(w http.ResponseWriter, r *http.Request) {
	c := auth.ClaimsFrom(r.Context())
	h.list(w, r, c.GameID, c.Subject)
}
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, r.PathValue("id"), "")
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request, gameID, playerID string) {
	id, ok := matchID(w, r)
	if !ok {
		return
	}
	m, err := h.Records.Get(r.Context(), gameID, id, playerID)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, 200, m)
}
func (h *Handler) PlayerGet(w http.ResponseWriter, r *http.Request) {
	c := auth.ClaimsFrom(r.Context())
	h.get(w, r, c.GameID, c.Subject)
}
func (h *Handler) AdminGet(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, r.PathValue("id"), "")
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MatchKey  string          `json:"match_key"`
		PlayerIDs []string        `json:"player_ids"`
		Metadata  json.RawMessage `json:"metadata"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	c := auth.ClaimsFrom(r.Context())
	m, err := h.Records.Create(r.Context(), r.PathValue("id"), req.MatchKey, req.PlayerIDs, req.Metadata, "admin:"+c.Subject)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, 200, m)
}
func (h *Handler) Finish(w http.ResponseWriter, r *http.Request) {
	id, ok := matchID(w, r)
	if !ok {
		return
	}
	var req struct {
		ExpectedRevision int64           `json:"expected_revision"`
		Status           string          `json:"status"`
		Result           json.RawMessage `json:"result"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	m, err := h.Records.Finish(r.Context(), r.PathValue("id"), id, req.ExpectedRevision, req.Status, req.Result)
	if err != nil {
		respondError(w, err)
		return
	}
	httpx.JSON(w, 200, m)
}
