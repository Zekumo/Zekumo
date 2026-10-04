package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Bans struct{ DB *pgxpool.Pool }

const banCols = `id, game_id, player_id, reason, operator, expires_at, created_at, lifted_at, lifted_by`

func scanBan(row pgx.Row) (*PlayerBan, error) {
	var b PlayerBan
	err := row.Scan(&b.ID, &b.GameID, &b.PlayerID, &b.Reason, &b.Operator,
		&b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

func (r Bans) Create(ctx context.Context, gameID, playerID, reason, operator string, expiresAt *time.Time) (*PlayerBan, error) {
	return scanBan(r.DB.QueryRow(ctx,
		`INSERT INTO player_bans (game_id, player_id, reason, operator, expires_at)
		 VALUES ($1,$2,$3,$4,$5) RETURNING `+banCols,
		gameID, playerID, reason, operator, expiresAt))
}

func (r Bans) Active(ctx context.Context, playerID string) (*PlayerBan, error) {
	return scanBan(r.DB.QueryRow(ctx,
		`SELECT `+banCols+` FROM player_bans
		 WHERE player_id=$1 AND lifted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		 ORDER BY created_at DESC LIMIT 1`, playerID))
}

func (r Bans) Lift(ctx context.Context, playerID, liftedBy string) (*PlayerBan, error) {
	return scanBan(r.DB.QueryRow(ctx,
		`UPDATE player_bans SET lifted_at = now(), lifted_by = $2
		 WHERE id IN (
		   SELECT id FROM player_bans
		   WHERE player_id=$1 AND lifted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		   ORDER BY created_at DESC LIMIT 1
		 ) RETURNING `+banCols, playerID, liftedBy))
}

func (r Bans) ByGame(ctx context.Context, gameID string, limit, offset int) ([]PlayerBan, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT b.id, b.game_id, b.player_id, b.reason, b.operator, b.expires_at,
		        b.created_at, b.lifted_at, b.lifted_by, p.nickname
		 FROM player_bans b JOIN players p ON p.id = b.player_id
		 WHERE b.game_id=$1 ORDER BY b.created_at DESC LIMIT $2 OFFSET $3`,
		gameID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerBan{}
	for rows.Next() {
		var b PlayerBan
		if err := rows.Scan(&b.ID, &b.GameID, &b.PlayerID, &b.Reason, &b.Operator,
			&b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy, &b.Nickname); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
