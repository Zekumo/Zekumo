package repo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Dialogues struct{ DB *pgxpool.Pool }

func (r Dialogues) Upsert(ctx context.Context, gameID, scriptKey, title string, content json.RawMessage) (*DialogueScript, error) {
	var d DialogueScript
	err := r.DB.QueryRow(ctx,
		`INSERT INTO dialogue_scripts (game_id, script_key, title, content)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (game_id, script_key) DO UPDATE
		   SET title = EXCLUDED.title, content = EXCLUDED.content,
		       version = dialogue_scripts.version + 1, updated_at = now()
		 RETURNING id, game_id, script_key, title, content, version, updated_at`,
		gameID, scriptKey, title, content,
	).Scan(&d.ID, &d.GameID, &d.ScriptKey, &d.Title, &d.Content, &d.Version, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r Dialogues) Get(ctx context.Context, gameID, scriptKey string) (*DialogueScript, error) {
	var d DialogueScript
	err := r.DB.QueryRow(ctx,
		`SELECT id, game_id, script_key, title, content, version, updated_at
		 FROM dialogue_scripts WHERE game_id = $1 AND script_key = $2`,
		gameID, scriptKey,
	).Scan(&d.ID, &d.GameID, &d.ScriptKey, &d.Title, &d.Content, &d.Version, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// List returns script metadata without content.
func (r Dialogues) List(ctx context.Context, gameID string) ([]DialogueScript, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT id, game_id, script_key, title, version, updated_at
		 FROM dialogue_scripts WHERE game_id = $1 ORDER BY script_key`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scripts := []DialogueScript{}
	for rows.Next() {
		var d DialogueScript
		if err := rows.Scan(&d.ID, &d.GameID, &d.ScriptKey, &d.Title, &d.Version, &d.UpdatedAt); err != nil {
			return nil, err
		}
		scripts = append(scripts, d)
	}
	return scripts, rows.Err()
}

func (r Dialogues) Delete(ctx context.Context, gameID, scriptKey string) error {
	tag, err := r.DB.Exec(ctx,
		`DELETE FROM dialogue_scripts WHERE game_id = $1 AND script_key = $2`, gameID, scriptKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
