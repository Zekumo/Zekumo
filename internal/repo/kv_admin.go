package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type KVNamespace struct {
	Name      string    `json:"name"`
	KeyCount  int64     `json:"key_count"`
	UpdatedAt time.Time `json:"updated_at"`
}
type KVAdminEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
	ExpiresAt *time.Time      `json:"expires_at"`
}

// Namespaces lists only keys belonging to the public signed-KV namespace format;
// arbitrary cloud-function keys are not silently reinterpreted or rewritten.
func (r GameData) Namespaces(ctx context.Context, gameID string, limit, offset int) ([]KVNamespace, error) {
	rows, err := r.DB.Query(ctx, `SELECT split_part(key,'/',1),count(*),max(updated_at) FROM game_data
 WHERE game_id=$1 AND (expires_at IS NULL OR expires_at>now())
 AND key ~ '^[a-zA-Z0-9_.-]{1,64}/[a-zA-Z0-9_.-]{1,64}$'
 GROUP BY split_part(key,'/',1) ORDER BY split_part(key,'/',1) LIMIT $2 OFFSET $3`, gameID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KVNamespace{}
	for rows.Next() {
		var n KVNamespace
		if err = rows.Scan(&n.Name, &n.KeyCount, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r GameData) AdminKeys(ctx context.Context, gameID, namespace string, limit, offset int) ([]KVAdminEntry, error) {
	prefix := namespace + "/"
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	rows, err := r.DB.Query(ctx, `SELECT substr(key,length($2::text)+1),updated_at,expires_at FROM game_data
 WHERE game_id=$1 AND key LIKE $3 || '%' ESCAPE '\' AND (expires_at IS NULL OR expires_at>now())
 AND substr(key,length($2::text)+1) ~ '^[a-zA-Z0-9_.-]{1,64}$'
 ORDER BY key LIMIT $4 OFFSET $5`, gameID, prefix, escaped, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KVAdminEntry{}
	for rows.Next() {
		var e KVAdminEntry
		if err = rows.Scan(&e.Key, &e.UpdatedAt, &e.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r GameData) AdminEntry(ctx context.Context, gameID, namespace, key string) (*KVAdminEntry, error) {
	e := &KVAdminEntry{Key: key}
	err := r.DB.QueryRow(ctx, `SELECT value,updated_at,expires_at FROM game_data
 WHERE game_id=$1 AND key=$2 AND (expires_at IS NULL OR expires_at>now())`, gameID, namespace+"/"+key).
		Scan(&e.Value, &e.UpdatedAt, &e.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}
