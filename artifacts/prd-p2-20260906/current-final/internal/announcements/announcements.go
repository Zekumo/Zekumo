// Package announcements manages server-broadcast messages published by the
// game operator. Clients poll for new entries using incremental IDs.
package announcements

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

// Handler exposes the announcements API over HTTP.
// No separate Service is needed: the logic is thin enough to live here.
type Handler struct {
	Repo repo.Announcements
}

// PublicList handles GET /v1/apps/{app_id}/announcements
// No authentication required — callers use ?after_id= for incremental polling.
func (h *Handler) PublicList(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("app_id")
	afterID := r.URL.Query().Get("after_id")
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))

	anns, err := h.Repo.PublicListByAppIDFiltered(r.Context(), appID, afterID, platform, channel, 50)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if anns == nil {
		anns = []repo.Announcement{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"announcements": anns})
}

// AdminList handles GET /admin/api/games/{id}/announcements
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	anns, err := h.Repo.AdminList(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if anns == nil {
		anns = []repo.Announcement{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"announcements": anns})
}

// AdminCreate handles POST /admin/api/games/{id}/announcements
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	var body struct {
		Title      string     `json:"title"`
		Body       string     `json:"body"`
		Importance string     `json:"importance"`
		Platform   string     `json:"platform"`
		Channel    string     `json:"channel"`
		ExpiresIn  *int       `json:"expires_in_secs"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "title is required")
		return
	}
	if body.ExpiresIn != nil && *body.ExpiresIn < 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_expiry", "expires_in_secs must be >= 0")
		return
	}
	if body.ExpiresAt != nil && !body.ExpiresAt.After(time.Now()) {
		httpx.Error(w, http.StatusBadRequest, "bad_expiry", "expires_at must be in the future")
		return
	}
	if body.Importance == "" {
		body.Importance = "info"
	}
	if !validImportance(body.Importance) {
		httpx.Error(w, http.StatusBadRequest, "bad_importance", "importance must be info, warning or critical")
		return
	}

	a := repo.Announcement{
		GameID:     gameID,
		Title:      strings.TrimSpace(body.Title),
		Body:       body.Body,
		Importance: body.Importance,
		Platform:   strings.TrimSpace(body.Platform),
		Channel:    strings.TrimSpace(body.Channel),
	}
	if body.ExpiresAt != nil {
		a.ExpiresAt = body.ExpiresAt
	} else if body.ExpiresIn != nil && *body.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(*body.ExpiresIn) * time.Second)
		a.ExpiresAt = &t
	}

	ann, err := h.Repo.Create(r.Context(), a)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, ann)
}

// AdminUpdate handles PUT /admin/api/announcements/{aid}
func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("aid")
	var body struct {
		Title        string     `json:"title"`
		Body         string     `json:"body"`
		Importance   string     `json:"importance"`
		Platform     string     `json:"platform"`
		Channel      string     `json:"channel"`
		ExpiresIn    *int       `json:"expires_in_secs"`
		ExpiresAt    *time.Time `json:"expires_at"`
		ClearExpires bool       `json:"clear_expires"`
	}
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "title is required")
		return
	}
	if body.ExpiresIn != nil && *body.ExpiresIn < 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_expiry", "expires_in_secs must be >= 0")
		return
	}
	if body.ExpiresAt != nil && !body.ExpiresAt.After(time.Now()) {
		httpx.Error(w, http.StatusBadRequest, "bad_expiry", "expires_at must be in the future")
		return
	}

	if body.Importance != "" && !validImportance(body.Importance) {
		httpx.Error(w, http.StatusBadRequest, "bad_importance", "importance must be info, warning or critical")
		return
	}
	// PUT keeps the existing expiry when no expiry field is supplied. This
	// prevents an ordinary text edit from silently turning a timed notice into
	// a permanent one.
	existing, err := h.Repo.ByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "announcement not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	importance := body.Importance
	if importance == "" {
		importance = existing.Importance
	}
	a := repo.Announcement{
		Title:      strings.TrimSpace(body.Title),
		Body:       body.Body,
		Importance: importance,
		Platform:   strings.TrimSpace(body.Platform),
		Channel:    strings.TrimSpace(body.Channel),
		ExpiresAt:  existing.ExpiresAt,
	}
	if body.ClearExpires {
		a.ExpiresAt = nil
	} else if body.ExpiresAt != nil {
		a.ExpiresAt = body.ExpiresAt
	} else if body.ExpiresIn != nil {
		if *body.ExpiresIn <= 0 {
			a.ExpiresAt = nil
		} else {
			t := time.Now().Add(time.Duration(*body.ExpiresIn) * time.Second)
			a.ExpiresAt = &t
		}
	}
	ann, err := h.Repo.Update(r.Context(), id, a)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "announcement not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, ann)
}

func validImportance(v string) bool {
	return v == "info" || v == "warning" || v == "critical"
}

// AdminDeactivate handles DELETE /admin/api/announcements/{aid}
// Soft-delete: sets active=false so existing IDs remain stable.
func (h *Handler) AdminDeactivate(w http.ResponseWriter, r *http.Request) {
	if err := h.Repo.Deactivate(r.Context(), r.PathValue("aid")); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "announcement not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
