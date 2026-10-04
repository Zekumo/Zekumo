package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Bans struct{ DB *pgxpool.Pool }

var ErrAlreadyBanned = errors.New("player already has an active ban")

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
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, playerID); err != nil {
		return nil, err
	}
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT true FROM player_bans
		 WHERE player_id=$1 AND lifted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		 LIMIT 1`, playerID).Scan(&exists)
	if err == nil {
		return nil, ErrAlreadyBanned
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	ban, err := scanBan(tx.QueryRow(ctx,
		`INSERT INTO player_bans (game_id, player_id, reason, operator, expires_at)
		 VALUES ($1,$2,$3,$4,$5) RETURNING `+banCols,
		gameID, playerID, reason, operator, expiresAt))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ban, nil
}

// CreateAndSet atomically records an active ban and sets the denormalized
// players.banned login guard.
func (r Bans) CreateAndSet(ctx context.Context, gameID, playerID, reason, operator string, expiresAt *time.Time) (*PlayerBan, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, playerID); err != nil {
		return nil, err
	}
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT true FROM player_bans
		 WHERE player_id=$1 AND lifted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		 LIMIT 1`, playerID).Scan(&exists)
	if err == nil {
		return nil, ErrAlreadyBanned
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	ban, err := scanBan(tx.QueryRow(ctx,
		`INSERT INTO player_bans (game_id, player_id, reason, operator, expires_at)
		 VALUES ($1,$2,$3,$4,$5) RETURNING `+banCols,
		gameID, playerID, reason, operator, expiresAt))
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE players SET banned=TRUE WHERE id=$1 AND game_id=$2`, playerID, gameID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ban, nil
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

// LiftAndClear atomically closes the active audit record and clears the
// denormalized players.banned login guard. Neither state can claim the player
// is unbanned unless both writes succeed.
func (r Bans) LiftAndClear(ctx context.Context, playerID, liftedBy string) (*PlayerBan, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ban, err := scanBan(tx.QueryRow(ctx,
		`UPDATE player_bans SET lifted_at = now(), lifted_by = $2
		 WHERE id IN (
		   SELECT id FROM player_bans
		   WHERE player_id=$1 AND lifted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		   ORDER BY created_at DESC LIMIT 1
		 ) RETURNING `+banCols, playerID, liftedBy))
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE players SET banned=FALSE WHERE id=$1`, playerID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ban, nil
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
