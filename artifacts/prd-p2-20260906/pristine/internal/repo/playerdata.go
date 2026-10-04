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
		`SELECT key, value, updated_at FROM player_data WHERE player_id = $1 AND key = $2`,
		playerID, key).Scan(&e.Key, &e.Value, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r PlayerData) Put(ctx context.Context, playerID, key string, value json.RawMessage) (*DataEntry, error) {
	var e DataEntry
	err := r.DB.QueryRow(ctx,
		`INSERT INTO player_data (player_id, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT (player_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		 RETURNING key, value, updated_at`,
		playerID, key, value).Scan(&e.Key, &e.Value, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r PlayerData) Delete(ctx context.Context, playerID, key string) error {
	tag, err := r.DB.Exec(ctx,
		`DELETE FROM player_data WHERE player_id = $1 AND key = $2`, playerID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns keys and timestamps without values (values can be large).
func (r PlayerData) List(ctx context.Context, playerID string) ([]DataEntry, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT key, updated_at FROM player_data WHERE player_id = $1 ORDER BY key`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []DataEntry{}
	for rows.Next() {
		var e DataEntry
		if err := rows.Scan(&e.Key, &e.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ListWithValues returns all entries including values, for admin inspection.
func (r PlayerData) ListWithValues(ctx context.Context, playerID string) ([]DataEntry, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT key, value, updated_at FROM player_data WHERE player_id = $1 ORDER BY key`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []DataEntry{}
	for rows.Next() {
		var e DataEntry
		if err := rows.Scan(&e.Key, &e.Value, &e.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
