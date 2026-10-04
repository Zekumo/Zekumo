// Package achievements implements the achievement system: game-level definitions
// managed via the admin API, and server-side unlock tracking per player.
package achievements

import (
	"errors"
	"net/http"
	"strings"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

// Emitter publishes platform events; nil disables emission.
type Emitter interface {
	Emit(gameID, event string, data any)
}

// Service holds business logic for the achievement system.
type Service struct {
	Defs    repo.AchievementDefs
	Unlocks repo.AchievementUnlocks
}

// Handler exposes the achievement API over HTTP.
type Handler struct {
	Svc    *Service
	Events Emitter // optional webhook bus
}

// achievementWithStatus combines a definition with the caller's unlock state.
type achievementWithStatus struct {
	repo.AchievementDef
	Unlocked   bool `json:"unlocked"`
	Progress   int  `json:"progress"`
	UnlockedAt *any `json:"unlocked_at,omitempty"`
}

// List handles GET /v1/achievements
// Returns all achievement definitions merged with the caller's unlock state.
// Hidden achievements that the caller has not unlocked are returned with
// name/description/icon redacted to prevent spoilers.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())

	defs, err := h.Svc.Defs.ByGame(r.Context(), claims.GameID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	unlockMap, err := h.Svc.Unlocks.UnlockMap(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	type entry struct {
		ID          string `json:"id"`
		Key         string `json:"key"`
		Name        string `json:"name"`
		Description string `json:"description"`
		IconURL     string `json:"icon_url"`
		Rarity      string `json:"rarity"`
		Hidden      bool   `json:"hidden"`
		Type        string `json:"type"`
		Target      int    `json:"target"`
		SortOrder   int    `json:"sort_order"`
		Unlocked    bool   `json:"unlocked"`
		Progress    int    `json:"progress"`
		UnlockedAt  any    `json:"unlocked_at"`
	}

	out := make([]entry, 0, len(defs))
	for _, d := range defs {
		u, hasProgress := unlockMap[d.ID]
		unlocked := hasProgress && u.UnlockedAt != nil

		e := entry{
			ID:        d.ID,
			Key:       d.Key,
			Rarity:    d.Rarity,
			Hidden:    d.Hidden,
			Type:      d.Type,
			Target:    d.Target,
			SortOrder: d.SortOrder,
			Unlocked:  unlocked,
		}
		if hasProgress {
			e.Progress = u.Progress
			e.UnlockedAt = u.UnlockedAt
		}
		// Redact details for hidden achievements the player has not yet unlocked.
		if d.Hidden && !unlocked {
			e.Name = "???"
			e.Description = ""
			e.IconURL = ""
		} else {
			e.Name = d.Name
			e.Description = d.Description
			e.IconURL = d.IconURL
		}
		out = append(out, e)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"achievements": out})
}

// Unlocked handles GET /v1/achievements/unlocked
// Returns only the achievements the caller has fully unlocked.
func (h *Handler) Unlocked(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())

	unlocks, err := h.Svc.Unlocks.UnlockedByPlayer(r.Context(), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Bulk-fetch definitions so we can return full details.
	defs, err := h.Svc.Defs.ByGame(r.Context(), claims.GameID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defMap := make(map[string]repo.AchievementDef, len(defs))
	for _, d := range defs {
		defMap[d.ID] = d
	}

	type unlockEntry struct {
		repo.AchievementDef
		Unlocked   bool `json:"unlocked"`
		Progress   int  `json:"progress"`
		UnlockedAt any  `json:"unlocked_at"`
	}
	out := make([]unlockEntry, 0, len(unlocks))
	for _, u := range unlocks {
		d, ok := defMap[u.AchievementID]
		if !ok {
			continue
		}
		out = append(out, unlockEntry{AchievementDef: d, Unlocked: true, Progress: u.Progress, UnlockedAt: u.UnlockedAt})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"unlocked": out})
}

// AdminList handles GET /admin/api/games/{id}/achievements
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	defs, err := h.Svc.Defs.ByGame(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if defs == nil {
		defs = []repo.AchievementDef{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"achievements": defs})
}

// AdminCreate handles POST /admin/api/games/{id}/achievements
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	var body repo.AchievementDef
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	if body.Key == "" || body.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "key and name are required")
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	body.Name = strings.TrimSpace(body.Name)
	if body.Key == "" || body.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "key and name are required")
		return
	}
	if body.Rarity == "" {
		body.Rarity = "common"
	}
	if body.Type == "" {
		body.Type = "instant"
	}
	if err := validateDefinition(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_definition", err.Error())
		return
	}
	def, err := h.Svc.Defs.Create(r.Context(), gameID, body)
	if err != nil {
		if repo.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "key_exists", "an achievement with this key already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, def)
}

// AdminUpdate handles PUT /admin/api/achievements/{aid}
func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("aid")
	var body repo.AchievementDef
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.IconURL = strings.TrimSpace(body.IconURL)
	if err := validateDefinition(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_definition", err.Error())
		return
	}
	def, transitions, err := h.Svc.Defs.UpdateWithTransitions(r.Context(), id, body)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "achievement not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if h.Events != nil {
		for _, unlock := range transitions {
			h.Events.Emit(def.GameID, "achievement.unlocked", map[string]any{
				"achievement_id":  id,
				"achievement_key": def.Key,
				"player_id":       unlock.PlayerID,
				"unlocked_at":     unlock.UnlockedAt,
			})
		}
	}
	httpx.JSON(w, http.StatusOK, def)
}

// AdminDelete handles DELETE /admin/api/achievements/{aid}
func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Defs.Delete(r.Context(), r.PathValue("aid")); err != nil {
		if errors.Is(err, repo.ErrAchievementUnlocked) {
			httpx.Error(w, http.StatusConflict, "achievement_unlocked", "an achievement with unlocks cannot be deleted")
			return
		}
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "achievement not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminUnlock handles POST /admin/api/achievements/{aid}/unlock
// Body: {"player_id": "...", "progress": 1}
// Progress defaults to the achievement's target (instant full unlock).
func (h *Handler) AdminUnlock(w http.ResponseWriter, r *http.Request) {
	achID := r.PathValue("aid")
	var body struct {
		PlayerID string `json:"player_id"`
		Progress *int   `json:"progress"`
	}
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	if body.PlayerID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "player_id is required")
		return
	}
	if body.Progress != nil && *body.Progress < 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_progress", "progress must be >= 0")
		return
	}

	// Fetch the definition to know the target.
	def, err := h.Svc.Defs.ByID(r.Context(), achID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "achievement not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	progress := def.Target // default: full unlock
	if body.Progress != nil {
		progress = *body.Progress
	}

	unlock, newlyUnlocked, err := h.Svc.Unlocks.UpsertWithTransition(r.Context(), achID, body.PlayerID, progress)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "achievement or player not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Emit webhook event when the achievement is newly unlocked.
	if h.Events != nil && newlyUnlocked {
		h.Events.Emit(def.GameID, "achievement.unlocked", map[string]any{
			"achievement_id":  achID,
			"achievement_key": def.Key,
			"player_id":       body.PlayerID,
			"unlocked_at":     unlock.UnlockedAt,
		})
	}
	httpx.JSON(w, http.StatusOK, unlock)
}

func validateDefinition(d *repo.AchievementDef) error {
	if d.Name == "" {
		return errors.New("name is required")
	}
	if d.Rarity == "" {
		d.Rarity = "common"
	}
	switch d.Rarity {
	case "common", "rare", "epic", "legendary":
	default:
		return errors.New("rarity must be common, rare, epic or legendary")
	}
	if d.Type == "" {
		d.Type = "instant"
	}
	switch d.Type {
	case "instant":
		d.Target = 1
	case "progress":
		if d.Target < 1 {
			return errors.New("progress achievements require target >= 1")
		}
	default:
		return errors.New("type must be instant or progress")
	}
	if d.Target < 1 {
		return errors.New("target must be >= 1")
	}
	return nil
}
