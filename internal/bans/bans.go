// Package bans implements player bans: an audit table in Postgres, a Redis
// blacklist that invalidates already-issued tokens, and admin endpoints.
package bans

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

type Emitter interface {
	Emit(gameID, event string, data any)
}

type Service struct {
	Realtime interface{ RevokePlayer(string, string) }
	Bans     repo.Bans
	Players  repo.Players
	RDB      *redis.Client
}

func banKey(playerID string) string { return "ban:" + playerID }

func (s *Service) TokenBanned(ctx context.Context, playerID string) bool {
	n, err := s.RDB.Exists(ctx, banKey(playerID)).Result()
	if err == nil {
		return n > 0
	}
	// Redis outages must not turn an existing durable ban into an auth bypass.
	_, dbErr := s.Bans.Active(ctx, playerID)
	return !errors.Is(dbErr, repo.ErrNotFound)
}

func (s *Service) ActiveBan(ctx context.Context, playerID string) *repo.PlayerBan {
	ban, err := s.Bans.Active(ctx, playerID)
	if err != nil {
		return nil
	}
	return ban
}

func (s *Service) Ban(ctx context.Context, playerID, reason, operator string, durationSecs int64) (*repo.PlayerBan, error) {
	p, err := s.Players.ByID(ctx, playerID)
	if err != nil {
		return nil, err
	}
	var expiresAt *time.Time
	ttl := time.Duration(0)
	if durationSecs > 0 {
		t := time.Now().Add(time.Duration(durationSecs) * time.Second)
		expiresAt = &t
		ttl = time.Until(t)
	}
	ban, err := s.Bans.Create(ctx, p.GameID, playerID, reason, operator, expiresAt)
	if err != nil {
		return nil, err
	}
	_ = s.Players.SetBanned(ctx, playerID, true)
	_ = s.RDB.Set(ctx, banKey(playerID), reason, ttl).Err()
	if s.Realtime != nil {
		s.Realtime.RevokePlayer(p.GameID, playerID)
	}
	notice, _ := json.Marshal(map[string]string{"game_id": p.GameID, "player_id": playerID})
	if err := s.RDB.Publish(ctx, realtimeBanChannel, notice).Err(); err != nil {
		log.Printf("bans: realtime fanout unavailable: %v", err)
	}
	return ban, nil
}

func (s *Service) Unban(ctx context.Context, playerID, liftedBy string) (*repo.PlayerBan, error) {
	ban, err := s.Bans.Lift(ctx, playerID, liftedBy)
	if err != nil {
		return nil, err
	}
	_ = s.Players.SetBanned(ctx, playerID, false)
	_ = s.RDB.Del(ctx, banKey(playerID)).Err()
	return ban, nil
}

type Handler struct {
	Svc    *Service
	Events Emitter
}

// Ban handles POST /admin/api/players/{pid}/ban
// Body: {"reason","duration_secs"?} — 0 or omitted means permanent.
func (h *Handler) Ban(w http.ResponseWriter, r *http.Request) {
	playerID := r.PathValue("pid")
	var req struct {
		Reason       string `json:"reason"`
		DurationSecs int64  `json:"duration_secs"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_reason", "reason is required")
		return
	}
	if req.DurationSecs < 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_duration", "duration_secs must be >= 0")
		return
	}
	if existing := h.Svc.ActiveBan(r.Context(), playerID); existing != nil {
		httpx.Error(w, http.StatusConflict, "already_banned", "player already has an active ban")
		return
	}
	operator := ""
	if claims := auth.ClaimsFrom(r.Context()); claims != nil {
		operator = claims.Subject
	}
	ban, err := h.Svc.Ban(r.Context(), playerID, req.Reason, operator, req.DurationSecs)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "player not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if h.Events != nil {
		h.Events.Emit(ban.GameID, "player.banned", map[string]any{
			"player_id": ban.PlayerID, "reason": ban.Reason,
			"expires_at": ban.ExpiresAt, "operator": ban.Operator,
		})
	}
	httpx.JSON(w, http.StatusCreated, ban)
}

// Unban handles POST /admin/api/players/{pid}/unban
func (h *Handler) Unban(w http.ResponseWriter, r *http.Request) {
	liftedBy := ""
	if claims := auth.ClaimsFrom(r.Context()); claims != nil {
		liftedBy = claims.Subject
	}
	ban, err := h.Svc.Unban(r.Context(), r.PathValue("pid"), liftedBy)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "no active ban for this player")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if h.Events != nil {
		h.Events.Emit(ban.GameID, "player.unbanned", map[string]any{
			"player_id": ban.PlayerID, "lifted_by": ban.LiftedBy,
		})
	}
	httpx.JSON(w, http.StatusOK, ban)
}

// List handles GET /admin/api/games/{id}/bans?limit=&offset=
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, err := h.Svc.Bans.ByGame(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bans": list})
}

const realtimeBanChannel = "zekumo:realtime:player-bans"

// StartRealtimeNotifications fans durable bans out to already-open sockets on
// other replicas. It does not distribute rooms or make Pub/Sub durable.
func (s *Service) StartRealtimeNotifications(ctx context.Context) {
	if s.RDB == nil || s.Realtime == nil {
		return
	}
	go func() {
		subscription := s.RDB.Subscribe(ctx, realtimeBanChannel)
		defer subscription.Close()
		messages := subscription.Channel(redis.WithChannelSize(256))
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if online, ok := s.Realtime.(interface{ OnlinePlayerIDs() []string }); ok {
					ids := online.OnlinePlayerIDs()
					queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
					active, err := s.Bans.ActivePlayers(queryCtx, ids)
					cancel()
					if err != nil {
						log.Printf("bans: realtime reconciliation failed: %v", err)
						continue
					}
					for _, ban := range active {
						s.Realtime.RevokePlayer(ban.GameID, ban.PlayerID)
					}
				}
			case message, ok := <-messages:
				if !ok {
					return
				}
				if len(message.Payload) > 1024 {
					continue
				}
				var notice struct {
					GameID   string `json:"game_id"`
					PlayerID string `json:"player_id"`
				}
				if json.Unmarshal([]byte(message.Payload), &notice) != nil || notice.GameID == "" || notice.PlayerID == "" {
					continue
				}
				verifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				ban, err := s.Bans.Active(verifyCtx, notice.PlayerID)
				cancel()
				// Ignore stale notices delivered after an unban and cross-game payloads.
				if err == nil && ban != nil && ban.GameID == notice.GameID {
					s.Realtime.RevokePlayer(notice.GameID, notice.PlayerID)
				}
			}
		}
	}()
}
