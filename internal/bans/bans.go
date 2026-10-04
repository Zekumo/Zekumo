// Package bans implements player bans: an audit table in Postgres, a Redis
// blacklist that invalidates already-issued tokens, and admin endpoints.
package bans

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

type Emitter interface {
	Emit(gameID, event string, data any)
}

type banStore interface {
	Active(ctx context.Context, playerID string) (*repo.PlayerBan, error)
	Create(ctx context.Context, gameID, playerID, reason, operator string, expiresAt *time.Time) (*repo.PlayerBan, error)
	CreateAndSet(ctx context.Context, gameID, playerID, reason, operator string, expiresAt *time.Time) (*repo.PlayerBan, error)
	Lift(ctx context.Context, playerID, liftedBy string) (*repo.PlayerBan, error)
	LiftAndClear(ctx context.Context, playerID, liftedBy string) (*repo.PlayerBan, error)
	ByGame(ctx context.Context, gameID string, limit, offset int) ([]repo.PlayerBan, error)
}

type banCache interface {
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

type Service struct {
	Bans    banStore
	Players repo.Players
	RDB     banCache
}

const (
	maxBanReasonLen       = 500
	maxBanDurationSeconds = int64(10 * 365 * 24 * 60 * 60)
	noBanCacheTTL         = 30 * time.Second
)

func banKey(playerID string) string   { return "ban:" + playerID }
func noBanKey(playerID string) string { return "ban:none:" + playerID }

func (s *Service) TokenBanned(ctx context.Context, playerID string) bool {
	n, cacheErr := s.RDB.Exists(ctx, banKey(playerID)).Result()
	if cacheErr == nil && n == 0 {
		if clear, err := s.RDB.Exists(ctx, noBanKey(playerID)).Result(); err == nil && clear > 0 {
			return false
		}
	}
	ban, dbErr := s.Bans.Active(ctx, playerID)
	if dbErr == nil && ban != nil {
		ttl := time.Duration(0)
		if ban.ExpiresAt != nil {
			ttl = time.Until(*ban.ExpiresAt)
			if ttl <= 0 {
				return false
			}
		}
		_ = s.RDB.Set(ctx, banKey(playerID), ban.Reason, ttl).Err()
		_ = s.RDB.Del(ctx, noBanKey(playerID)).Err()
		return true
	}
	if errors.Is(dbErr, repo.ErrNotFound) {
		// A successful unban must win even if Redis deletion failed. Clear a
		// stale positive cache entry opportunistically.
		if n > 0 {
			_ = s.RDB.Del(ctx, banKey(playerID)).Err()
		}
		_ = s.RDB.Set(ctx, noBanKey(playerID), "1", noBanCacheTTL).Err()
		return false
	}
	// If both the cache and authoritative lookup are unavailable, fail closed:
	// allowing a possibly banned token would violate immediate revocation.
	log.Printf("bans: blacklist verification failed for %s: cache=%v db=%v", playerID, cacheErr, dbErr)
	return true
}

func (s *Service) ActiveBan(ctx context.Context, playerID string) (*repo.PlayerBan, error) {
	return s.Bans.Active(ctx, playerID)
}

func banExpiry(now time.Time, durationSecs int64) (*time.Time, time.Duration, error) {
	if durationSecs < 0 || durationSecs > maxBanDurationSeconds {
		return nil, 0, errors.New("duration_secs is outside the supported range")
	}
	if durationSecs == 0 {
		return nil, 0, nil
	}
	ttl := time.Duration(durationSecs) * time.Second
	expiresAt := now.Add(ttl)
	return &expiresAt, ttl, nil
}

func (s *Service) Ban(ctx context.Context, playerID, reason, operator string, durationSecs int64) (*repo.PlayerBan, error) {
	p, err := s.Players.ByID(ctx, playerID)
	if err != nil {
		return nil, err
	}
	expiresAt, ttl, err := banExpiry(time.Now(), durationSecs)
	if err != nil {
		return nil, err
	}
	ban, err := s.Bans.CreateAndSet(ctx, p.GameID, playerID, reason, operator, expiresAt)
	if err != nil {
		return nil, err
	}
	if err := s.RDB.Set(ctx, banKey(playerID), reason, ttl).Err(); err != nil {
		_, rollbackErr := s.Bans.LiftAndClear(ctx, playerID, "system:rollback")
		_ = s.RDB.Del(ctx, banKey(playerID)).Err()
		if rollbackErr != nil {
			return nil, fmt.Errorf("set token blacklist: %w (database rollback failed: %v)", err, rollbackErr)
		}
		return nil, fmt.Errorf("set token blacklist: %w", err)
	}
	_ = s.RDB.Del(ctx, noBanKey(playerID)).Err()
	return ban, nil
}

func (s *Service) Unban(ctx context.Context, playerID, liftedBy string) (*repo.PlayerBan, error) {
	ban, err := s.Bans.LiftAndClear(ctx, playerID, liftedBy)
	if err != nil {
		return nil, err
	}
	if err := s.RDB.Del(ctx, banKey(playerID)).Err(); err != nil {
		// The database is authoritative and TokenBanned verifies positive cache
		// hits, so this stale entry cannot keep the player banned.
		log.Printf("bans: clear token blacklist failed for %s: %v", playerID, err)
	}
	_ = s.RDB.Set(ctx, noBanKey(playerID), "1", noBanCacheTTL).Err()
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
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_reason", "reason is required")
		return
	}
	if utf8.RuneCountInString(req.Reason) > maxBanReasonLen {
		httpx.Error(w, http.StatusBadRequest, "bad_reason", "reason must be at most 500 characters")
		return
	}
	if _, _, err := banExpiry(time.Now(), req.DurationSecs); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_duration", "duration_secs must be 0 (permanent) or at most 10 years")
		return
	}
	existing, err := h.Svc.ActiveBan(r.Context(), playerID)
	if err == nil && existing != nil {
		httpx.Error(w, http.StatusConflict, "already_banned", "player already has an active ban")
		return
	}
	if err == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "ban_check_unavailable", "unable to verify current ban status")
		return
	}
	if !errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusServiceUnavailable, "ban_check_unavailable", "unable to verify current ban status")
		return
	}
	operator := ""
	if claims := auth.ClaimsFrom(r.Context()); claims != nil {
		operator = claims.Subject
	}
	ban, err := h.Svc.Ban(r.Context(), playerID, req.Reason, operator, req.DurationSecs)
	if err != nil {
		if errors.Is(err, repo.ErrAlreadyBanned) {
			httpx.Error(w, http.StatusConflict, "already_banned", "player already has an active ban")
			return
		}
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
	if offset < 0 {
		offset = 0
	}
	list, err := h.Svc.Bans.ByGame(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bans": list})
}
