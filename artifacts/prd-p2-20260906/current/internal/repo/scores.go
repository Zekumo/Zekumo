package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Scores is the durable copy of leaderboard scores; ranking reads go through Redis.
type Scores struct{ DB *pgxpool.Pool }

type ScoreRow struct {
	PlayerID string `json:"player_id"`
	Score    int64  `json:"score"`
}

// Upsert writes the authoritative score (already resolved by the leaderboard service).
func (r Scores) Upsert(ctx context.Context, gameID, board, playerID string, score int64) error {
	_, err := r.DB.Exec(ctx,
		`INSERT INTO scores (game_id, board, player_id, score) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (game_id, board, player_id) DO UPDATE SET score = EXCLUDED.score, updated_at = now()`,
		gameID, board, playerID, score)
	return err
}

type BoardSummary struct {
	Board     string    `json:"board"`
	Players   int       `json:"players"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Boards lists the leaderboards a game has scores on. Boards are created
// implicitly by the first submission, so this is the only way to discover them.
func (r Scores) Boards(ctx context.Context, gameID string) ([]BoardSummary, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT board, count(*), max(updated_at) FROM scores WHERE game_id = $1
		 GROUP BY board ORDER BY board`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boards := []BoardSummary{}
	for rows.Next() {
		var b BoardSummary
		if err := rows.Scan(&b.Board, &b.Players, &b.UpdatedAt); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

// All returns every score for a board, used to rebuild the Redis zset.
func (r Scores) All(ctx context.Context, gameID, board string) ([]ScoreRow, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT player_id, score FROM scores WHERE game_id = $1 AND board = $2`, gameID, board)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScoreRow{}
	for rows.Next() {
		var s ScoreRow
		if err := rows.Scan(&s.PlayerID, &s.Score); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
