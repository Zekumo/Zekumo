package dialogue

import (
	"errors"
	"net/http"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

// Handler serves read-only story dialogue APIs to game clients.
// Writing goes through the admin API.
type Handler struct{ Dialogues repo.Dialogues }

// List handles GET /v1/dialogues (metadata only, so clients can diff versions).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	scripts, err := h.Dialogues.List(r.Context(), claims.GameID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scripts": scripts})
}

// Get handles GET /v1/dialogues/{key} and returns the full script content.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	script, err := h.Dialogues.Get(r.Context(), claims.GameID, r.PathValue("key"))
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
