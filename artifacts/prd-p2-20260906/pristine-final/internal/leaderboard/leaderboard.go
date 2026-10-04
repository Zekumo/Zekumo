package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/redis/go-redis/v9"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

var boardName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

// Service ranks through Redis zsets and mirrors scores to Postgres for durability.
// If the zset is missing (Redis flushed), it is rebuilt lazily from Postgres.
type Service struct {
	RDB     *redis.Client
	Scores  repo.Scores
	Players repo.Players
}

func key(gameID, board string) string { return "lb:" + gameID + ":" + board }

func (s *Service) ensure(ctx context.Context, gameID, board string) error {
	n, err := s.RDB.Exists(ctx, key(gameID, board)).Result()
	if err != nil || n > 0 {
		return err
	}
	rows, err := s.Scores.All(ctx, gameID, board)
	if err != nil || len(rows) == 0 {
		return err
	}
	members := make([]redis.Z, len(rows))
	for i, r := range rows {
		members[i] = redis.Z{Score: float64(r.Score), Member: r.PlayerID}
	}
	return s.RDB.ZAdd(ctx, key(gameID, board), members...).Err()
}

// Submit applies a score with the given mode and returns the new value.
// Modes: "max" keeps the best score (default), "incr" adds, "replace" overwrites.
func (s *Service) Submit(ctx context.Context, gameID, board, playerID string, score int64, mode string) (int64, error) {
	if err := s.ensure(ctx, gameID, board); err != nil {
		return 0, err
	}
	k := key(gameID, board)
	var newScore int64
	switch mode {
	case "", "max":
		if err := s.RDB.ZAddGT(ctx, k, redis.Z{Score: float64(score), Member: playerID}).Err(); err != nil {
			return 0, err
		}
		f, err := s.RDB.ZScore(ctx, k, playerID).Result()
		if err != nil {
			return 0, err
		}
		newScore = int64(f)
	case "incr":
		f, err := s.RDB.ZIncrBy(ctx, k, float64(score), playerID).Result()
		if err != nil {
			return 0, err
		}
		newScore = int64(f)
	case "replace":
		if err := s.RDB.ZAdd(ctx, k, redis.Z{Score: float64(score), Member: playerID}).Err(); err != nil {
			return 0, err
		}
		newScore = score
	default:
		return 0, fmt.Errorf("unknown mode %q (want max, incr or replace)", mode)
	}
	if err := s.Scores.Upsert(ctx, gameID, board, playerID, newScore); err != nil {
		return 0, err
	}
	return newScore, nil
}

type Entry struct {
	Rank     int64  `json:"rank"`
	PlayerID string `json:"player_id"`
	Nickname string `json:"nickname"`
	Score    int64  `json:"score"`
}

func (s *Service) Top(ctx context.Context, gameID, board string, offset, limit int64) ([]Entry, error) {
	if err := s.ensure(ctx, gameID, board); err != nil {
		return nil, err
	}
	zs, err := s.RDB.ZRevRangeWithScores(ctx, key(gameID, board), offset, offset+limit-1).Result()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(zs))
	ids := make([]string, len(zs))
	for i, z := range zs {
		id := z.Member.(string)
		ids[i] = id
		entries[i] = Entry{Rank: offset + int64(i) + 1, PlayerID: id, Score: int64(z.Score)}
	}
	if len(ids) > 0 {
		nicks, err := s.Players.Nicknames(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range entries {
			entries[i].Nickname = nicks[entries[i].PlayerID]
		}
	}
	return entries, nil
}

// Me returns the caller's rank and score, or nil if they have no score yet.
func (s *Service) Me(ctx context.Context, gameID, board, playerID string) (*Entry, error) {
	if err := s.ensure(ctx, gameID, board); err != nil {
		return nil, err
	}
	k := key(gameID, board)
	rank, err := s.RDB.ZRevRank(ctx, k, playerID).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	score, err := s.RDB.ZScore(ctx, k, playerID).Result()
	if err != nil {
		return nil, err
	}
	return &Entry{Rank: rank + 1, PlayerID: playerID, Score: int64(score)}, nil
}

type Handler struct {
	Svc    *Service
	Events Emitter // optional webhook bus
}

// Emitter publishes platform events; nil disables emission.
type Emitter interface {
	Emit(gameID, event string, data any)
}

// SubmitScore handles POST /v1/leaderboards/{board}/score with {score, mode?}.
func (h *Handler) SubmitScore(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	board := r.PathValue("board")
	if !boardName.MatchString(board) {
		httpx.Error(w, http.StatusBadRequest, "bad_board", "board name must match [a-zA-Z0-9_.-]{1,64}")
		return
	}
	var req struct {
		Score int64  `json:"score"`
		Mode  string `json:"mode"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	newScore, err := h.Svc.Submit(r.Context(), claims.GameID, board, claims.Subject, req.Score, req.Mode)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "submit_failed", err.Error())
		return
	}
	if h.Events != nil {
		h.Events.Emit(claims.GameID, "leaderboard.score", map[string]any{
			"board": board, "player_id": claims.Subject, "score": newScore,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]int64{"score": newScore})
}

// Top handles GET /v1/leaderboards/{board}?offset=&limit=.
func (h *Handler) Top(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	board := r.PathValue("board")
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	entries, err := h.Svc.Top(r.Context(), claims.GameID, board, max(offset, 0), limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// AdminBoards handles GET /admin/api/games/{id}/leaderboards.
func (h *Handler) AdminBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := h.Svc.Scores.Boards(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"boards": boards})
}

// AdminTop handles GET /admin/api/games/{id}/leaderboards/{board}?limit=.
func (h *Handler) AdminTop(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	entries, err := h.Svc.Top(r.Context(), r.PathValue("id"), r.PathValue("board"), 0, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// Me handles GET /v1/leaderboards/{board}/me.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	entry, err := h.Svc.Me(r.Context(), claims.GameID, r.PathValue("board"), claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if entry == nil {
		httpx.Error(w, http.StatusNotFound, "no_score", "you have no score on this board yet")
		return
	}
	httpx.JSON(w, http.StatusOK, entry)
}
