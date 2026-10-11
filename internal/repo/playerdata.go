package repo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PlayerData struct{ DB *pgxpool.Pool }

func (r PlayerData) Get(ctx context.Context, playerID, key string) (*DataEntry, error) {
	var e DataEntry
	err := r.DB.QueryRow(ctx,
		`SELECT key, value, updated_at, revision::text FROM player_data WHERE player_id = $1 AND key = $2`,
		playerID, key).Scan(&e.Key, &e.Value, &e.UpdatedAt, &e.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ErrSaveConflict means the supplied precondition did not match atomically.
var ErrSaveConflict = errors.New("save revision precondition failed")

// SaveCondition optionally requires a revision ("*" means any existing save)
// or a previously absent slot. The zero value preserves unconditional writes.
type SaveCondition struct {
	Revision     string
	OnlyIfAbsent bool
}

func (r PlayerData) Put(ctx context.Context, playerID, key string, value json.RawMessage) (*DataEntry, error) {
	return r.PutConditional(ctx, playerID, key, value, SaveCondition{})
}

func (r PlayerData) PutConditional(ctx context.Context, playerID, key string, value json.RawMessage, condition SaveCondition) (*DataEntry, error) {
	if condition.OnlyIfAbsent && condition.Revision != "" {
		return nil, errors.New("conflicting save conditions")
	}
	var e DataEntry
	var row pgx.Row
	switch {
	case condition.OnlyIfAbsent:
		row = r.DB.QueryRow(ctx, `INSERT INTO player_data (player_id, key, value) VALUES ($1,$2,$3)
   ON CONFLICT (player_id,key) DO NOTHING RETURNING key,value,updated_at,revision::text`, playerID, key, value)
	case condition.Revision == "*":
		row = r.DB.QueryRow(ctx, `UPDATE player_data SET value=$3,updated_at=now()
   WHERE player_id=$1 AND key=$2 RETURNING key,value,updated_at,revision::text`, playerID, key, value)
	case condition.Revision != "":
		row = r.DB.QueryRow(ctx, `UPDATE player_data SET value=$3,updated_at=now()
   WHERE player_id=$1 AND key=$2 AND revision=$4::uuid RETURNING key,value,updated_at,revision::text`, playerID, key, value, condition.Revision)
	default:
		row = r.DB.QueryRow(ctx, `INSERT INTO player_data (player_id,key,value) VALUES ($1,$2,$3)
   ON CONFLICT (player_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()
   RETURNING key,value,updated_at,revision::text`, playerID, key, value)
	}
	err := row.Scan(&e.Key, &e.Value, &e.UpdatedAt, &e.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSaveConflict
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r PlayerData) Delete(ctx context.Context, playerID, key string) error {
	return r.DeleteConditional(ctx, playerID, key, "")
}

// DeleteConditional atomically checks the revision and deletes the row. A
// missing row fails a supplied precondition; legacy deletes return ErrNotFound.
func (r PlayerData) DeleteConditional(ctx context.Context, playerID, key, revision string) error {
	query := `DELETE FROM player_data WHERE player_id=$1 AND key=$2`
	args := []any{playerID, key}
	if revision != "" && revision != "*" {
		query += ` AND revision=$3::uuid`
		args = append(args, revision)
	}
	tag, err := r.DB.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if revision != "" {
			return ErrSaveConflict
		}
		return ErrNotFound
	}
	return nil
}

// List returns keys and timestamps without values (values can be large).
func (r PlayerData) List(ctx context.Context, playerID string) ([]DataEntry, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT key, updated_at, revision::text FROM player_data WHERE player_id = $1 ORDER BY key`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []DataEntry{}
	for rows.Next() {
		var e DataEntry
		if err := rows.Scan(&e.Key, &e.UpdatedAt, &e.Revision); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ListWithValues returns all entries including values, for admin inspection.
func (r PlayerData) ListWithValues(ctx context.Context, playerID string) ([]DataEntry, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT key, value, updated_at, revision::text FROM player_data WHERE player_id = $1 ORDER BY key`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []DataEntry{}
	for rows.Next() {
		var e DataEntry
		if err := rows.Scan(&e.Key, &e.Value, &e.UpdatedAt, &e.Revision); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
