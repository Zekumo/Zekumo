package player

import (
	"encoding/json"
	"errors"
	"net/http"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

const maxValueSize = 256 << 10 // 256KB per save slot

type Handler struct {
	Players repo.Players
	Data    repo.PlayerData
}

// GetProfile handles GET /v1/player/profile.
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	player, err := h.Players.ByID(r.Context(), claims.Subject)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "player not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, player)
}

// UpdateProfile handles PUT /v1/player/profile with {nickname?, profile?}.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		Nickname string          `json:"nickname"`
		Profile  json.RawMessage `json:"profile"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if len(req.Nickname) > 64 {
		httpx.Error(w, http.StatusBadRequest, "nickname_too_long", "nickname must be at most 64 characters")
		return
	}
	player, err := h.Players.UpdateProfile(r.Context(), claims.Subject, req.Nickname, req.Profile)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, player)
}

// ListData handles GET /v1/player/data (keys and timestamps only).
func (h *Handler) ListData(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	entries, err := h.Data.List(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// GetData handles GET /v1/player/data/{key}.
func (h *Handler) GetData(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	entry, err := h.Data.Get(r.Context(), claims.Subject, r.PathValue("key"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no data under this key")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, entry)
}

// PutData handles PUT /v1/player/data/{key}; the body is the JSON value itself.
func (h *Handler) PutData(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var value json.RawMessage
	if httpx.Decode(w, r, &value) != nil {
		return
	}
	if len(value) > maxValueSize {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "value_too_large", "value exceeds 256KB")
		return
	}
	entry, err := h.Data.Put(r.Context(), claims.Subject, r.PathValue("key"), value)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, entry)
}

// DeleteData handles DELETE /v1/player/data/{key}.
func (h *Handler) DeleteData(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	err := h.Data.Delete(r.Context(), claims.Subject, r.PathValue("key"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no data under this key")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
