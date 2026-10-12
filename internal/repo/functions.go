package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CloudFunction struct {
	ID        string    `json:"id"`
	GameID    string    `json:"game_id"`
	Name      string    `json:"name"`
	Code      string    `json:"code,omitempty"`
	Enabled   bool      `json:"enabled"`
	Public    bool      `json:"public"`
	CronSecs  int       `json:"cron_secs"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Functions struct{ DB *pgxpool.Pool }

const fnCols = `id, game_id, name, code, enabled, public, cron_secs, updated_at, revision`

func scanFn(row pgx.Row) (*CloudFunction, error) {
	var f CloudFunction
	err := row.Scan(&f.ID, &f.GameID, &f.Name, &f.Code, &f.Enabled, &f.Public, &f.CronSecs, &f.UpdatedAt, &f.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (r Functions) Upsert(ctx context.Context, gameID, name, code string, enabled, public bool, cronSecs int) (*CloudFunction, error) {
	return r.Save(ctx, gameID, name, code, enabled, public, cronSecs, nil)
}

// Save uses an optional expected revision to prevent overwriting another editor.
// Revision zero means create-only. A mismatched or removed record is a conflict.
func (r Functions) Save(ctx context.Context, gameID, name, code string, enabled, public bool, cronSecs int, expected *int64) (*CloudFunction, error) {
	if expected != nil && *expected > 0 {
		f, err := scanFn(r.DB.QueryRow(ctx,
			`UPDATE cloud_functions SET code=$3, enabled=$4, public=$5, cron_secs=$6
     WHERE game_id=$1 AND name=$2 AND revision=$7 RETURNING `+fnCols,
			gameID, name, code, enabled, public, cronSecs, *expected))
		if errors.Is(err, ErrNotFound) {
			return nil, ErrFunctionConflict
		}
		return f, err
	}
	f, err := scanFn(r.DB.QueryRow(ctx,
		`INSERT INTO cloud_functions (game_id, name, code, enabled, public, cron_secs)
   VALUES ($1,$2,$3,$4,$5,$6)
   ON CONFLICT (game_id,name) DO UPDATE SET code=EXCLUDED.code,
     enabled=EXCLUDED.enabled, public=EXCLUDED.public, cron_secs=EXCLUDED.cron_secs
   WHERE $7::boolean
   RETURNING `+fnCols, gameID, name, code, enabled, public, cronSecs, expected == nil))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrFunctionConflict
	}
	return f, err
}

var ErrFunctionConflict = errors.New("function changed; reload before saving")

type FunctionVersion struct {
	Revision  int64     `json:"revision"`
	Code      string    `json:"code,omitempty"`
	Enabled   bool      `json:"enabled"`
	Public    bool      `json:"public"`
	CronSecs  int       `json:"cron_secs"`
	CreatedAt time.Time `json:"created_at"`
}

func (r Functions) Versions(ctx context.Context, gameID, name string) ([]FunctionVersion, error) {
	rows, err := r.DB.Query(ctx, `SELECT v.revision, v.enabled, v.public, v.cron_secs, v.created_at
 FROM cloud_function_versions v JOIN cloud_functions f ON f.id=v.function_id
 WHERE f.game_id=$1 AND f.name=$2 ORDER BY v.revision DESC LIMIT 50`, gameID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FunctionVersion{}
	for rows.Next() {
		var v FunctionVersion
		if err := rows.Scan(&v.Revision, &v.Enabled, &v.Public, &v.CronSecs, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r Functions) Version(ctx context.Context, gameID, name string, revision int64) (*FunctionVersion, error) {
	var v FunctionVersion
	err := r.DB.QueryRow(ctx, `SELECT v.revision,v.code,v.enabled,v.public,v.cron_secs,v.created_at
 FROM cloud_function_versions v JOIN cloud_functions f ON f.id=v.function_id
 WHERE f.game_id=$1 AND f.name=$2 AND v.revision=$3`, gameID, name, revision).
		Scan(&v.Revision, &v.Code, &v.Enabled, &v.Public, &v.CronSecs, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, err
}

// Restore appends a fresh snapshot; it never changes or reuses historical IDs.
func (r Functions) Restore(ctx context.Context, gameID, name string, revision, expected int64) (*CloudFunction, error) {
	v, err := r.Version(ctx, gameID, name, revision)
	if err != nil {
		return nil, err
	}
	return r.Save(ctx, gameID, name, v.Code, v.Enabled, v.Public, v.CronSecs, &expected)
}

func (r Functions) Get(ctx context.Context, gameID, name string) (*CloudFunction, error) {
	return scanFn(r.DB.QueryRow(ctx,
		`SELECT `+fnCols+` FROM cloud_functions WHERE game_id = $1 AND name = $2`, gameID, name))
}

// List returns function metadata without code bodies.
func (r Functions) List(ctx context.Context, gameID string) ([]CloudFunction, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT id, game_id, name, '', enabled, public, cron_secs, updated_at, revision
		 FROM cloud_functions WHERE game_id = $1 ORDER BY name`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fns := []CloudFunction{}
	for rows.Next() {
		f, err := scanFn(rows)
		if err != nil {
			return nil, err
		}
		fns = append(fns, *f)
	}
	return fns, rows.Err()
}

// ListCron returns every enabled function with a timer, across all games.
func (r Functions) ListCron(ctx context.Context) ([]CloudFunction, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+fnCols+` FROM cloud_functions WHERE enabled AND cron_secs > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fns := []CloudFunction{}
	for rows.Next() {
		f, err := scanFn(rows)
		if err != nil {
			return nil, err
		}
		fns = append(fns, *f)
	}
	return fns, rows.Err()
}

func (r Functions) Delete(ctx context.Context, gameID, name string) error {
	tag, err := r.DB.Exec(ctx,
		`DELETE FROM cloud_functions WHERE game_id = $1 AND name = $2`, gameID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GameData is a per-game key-value store, mainly for cloud functions.
type GameData struct{ DB *pgxpool.Pool }

func (r GameData) Get(ctx context.Context, gameID, key string) (json.RawMessage, error) {
	var v json.RawMessage
	err := r.DB.QueryRow(ctx,
		`SELECT value FROM game_data
		 WHERE game_id = $1 AND key = $2 AND (expires_at IS NULL OR expires_at > now())`,
		gameID, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (r GameData) Put(ctx context.Context, gameID, key string, value json.RawMessage) error {
	return r.PutTTL(ctx, gameID, key, value, nil)
}

// PutTTL upserts a key; a nil expiry makes it permanent (any write without a
// TTL clears an earlier one).
func (r GameData) PutTTL(ctx context.Context, gameID, key string, value json.RawMessage, expiresAt *time.Time) error {
	_, err := r.DB.Exec(ctx,
		`INSERT INTO game_data (game_id, key, value, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (game_id, key) DO UPDATE
		   SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at, updated_at = now()`,
		gameID, key, value, expiresAt)
	return err
}

func (r GameData) Delete(ctx context.Context, gameID, key string) error {
	_, err := r.DB.Exec(ctx, `DELETE FROM game_data WHERE game_id = $1 AND key = $2`, gameID, key)
	return err
}

// ListPrefix returns live keys under a prefix; returned keys have the prefix
// stripped. Values are omitted.
func (r GameData) ListPrefix(ctx context.Context, gameID, prefix string, limit, offset int) ([]DataEntry, error) {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	rows, err := r.DB.Query(ctx,
		`SELECT substr(key, length($2::text) + 1), updated_at FROM game_data
		 WHERE game_id = $1 AND key LIKE $3 || '%' ESCAPE '\'
		   AND (expires_at IS NULL OR expires_at > now())
		 ORDER BY key LIMIT $4 OFFSET $5`,
		gameID, prefix, escaped, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DataEntry{}
	for rows.Next() {
		var e DataEntry
		if err := rows.Scan(&e.Key, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r GameData) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.DB.Exec(ctx, `DELETE FROM game_data WHERE expires_at IS NOT NULL AND expires_at <= now()`)
	return tag.RowsAffected(), err
}
