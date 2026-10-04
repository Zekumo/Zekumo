// Package friends implements the friend system: requests, accepted friendships,
// and per-player blocking. Online status is resolved via the realtime Hub.
package friends

import (
	"errors"
	"net/http"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

// OnlineChecker reports whether a player is currently connected via WebSocket.
type OnlineChecker interface {
	IsOnline(gameID, playerID string) bool
}

// Service holds the business logic for friend relationships.
type Service struct {
	Requests    repo.FriendRequests
	Friendships repo.Friendships
	Blocks      repo.PlayerBlocks
	Players     repo.Players
}

// Handler exposes the friend API over HTTP.
type Handler struct {
	Svc *Service
	Hub OnlineChecker // may be nil in tests
}

// SendRequest handles POST /v1/friends/request
// Body: {"target_player_id": "..."}
func (h *Handler) SendRequest(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		TargetPlayerID string `json:"target_player_id"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.TargetPlayerID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "target_player_id is required")
		return
	}
	if req.TargetPlayerID == claims.Subject {
		httpx.Error(w, http.StatusBadRequest, "self_request", "cannot send friend request to yourself")
		return
	}

	// Check target exists in this game.
	if _, err := h.Svc.Players.ByID(r.Context(), req.TargetPlayerID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "player_not_found", "target player not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Refuse if either party has blocked the other.
	blocked, err := h.Svc.Blocks.EitherBlocked(r.Context(), claims.GameID, claims.Subject, req.TargetPlayerID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if blocked {
		httpx.Error(w, http.StatusForbidden, "blocked", "cannot send friend request")
		return
	}

	// Already friends?
	if already, err := h.Svc.Friendships.Exists(r.Context(), claims.GameID, claims.Subject, req.TargetPlayerID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	} else if already {
		httpx.Error(w, http.StatusConflict, "already_friends", "already friends with this player")
		return
	}

	fr, err := h.Svc.Requests.Create(r.Context(), claims.GameID, claims.Subject, req.TargetPlayerID)
	if err != nil {
		if repo.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "request_exists", "a pending request already exists")
			return
		}
		// ErrNotFound here means no row was returned (e.g. declined row was
		// re-inserted by another caller simultaneously) — treat as conflict.
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusConflict, "request_exists", "a pending request already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, fr)
}

// Accept handles POST /v1/friends/accept
// Body: {"request_id": "..."}
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		RequestID string `json:"request_id"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}

	fr, err := h.Svc.Requests.ByID(r.Context(), req.RequestID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "request_not_found", "friend request not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// Only the recipient may accept.
	if fr.ToID != claims.Subject {
		httpx.Error(w, http.StatusForbidden, "forbidden", "only the recipient can accept this request")
		return
	}

	if err := h.Svc.Requests.Accept(r.Context(), fr.ID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// Create the symmetric friendship row.
	if err := h.Svc.Friendships.Add(r.Context(), claims.GameID, fr.FromID, fr.ToID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// Decline handles POST /v1/friends/decline
// Body: {"request_id": "..."}  — works for both recipient rejection and sender retraction.
func (h *Handler) Decline(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		RequestID string `json:"request_id"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if err := h.Svc.Requests.Decline(r.Context(), req.RequestID, claims.Subject); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "request_not_found", "pending request not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "declined"})
}

// List handles GET /v1/friends — returns the caller's friend list with online status.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	rows, err := h.Svc.Friendships.List(r.Context(), claims.GameID, claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	friends := make([]repo.Friend, 0, len(rows))
	for _, row := range rows {
		online := h.Hub != nil && h.Hub.IsOnline(claims.GameID, row.PlayerID)
		friends = append(friends, repo.Friend{
			PlayerID:    row.PlayerID,
			Nickname:    row.Nickname,
			Online:      online,
			FriendSince: row.FriendSince,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"friends": friends})
}

// ListRequests handles GET /v1/friends/requests — pending requests in both directions.
func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	reqs, err := h.Svc.Requests.List(r.Context(), claims.GameID, claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if reqs == nil {
		reqs = []repo.FriendRequest{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"requests": reqs})
}

// Remove handles DELETE /v1/friends/{player_id} — unfriend.
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	targetID := r.PathValue("player_id")
	if err := h.Svc.Friendships.Remove(r.Context(), claims.GameID, claims.Subject, targetID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := h.Svc.Requests.DeleteBetween(r.Context(), claims.GameID, claims.Subject, targetID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// Block handles POST /v1/friends/block
// Body: {"target_player_id": "..."}
// Also removes any existing friendship.
func (h *Handler) Block(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	var req struct {
		TargetPlayerID string `json:"target_player_id"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.TargetPlayerID == "" || req.TargetPlayerID == claims.Subject {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid target_player_id")
		return
	}
	// Blocking ends any friendship and clears outstanding request history.
	if err := h.Svc.Friendships.Remove(r.Context(), claims.GameID, claims.Subject, req.TargetPlayerID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := h.Svc.Requests.DeleteBetween(r.Context(), claims.GameID, claims.Subject, req.TargetPlayerID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := h.Svc.Blocks.Block(r.Context(), claims.GameID, claims.Subject, req.TargetPlayerID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "blocked"})
}
