package repo

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type FunctionDraft struct {
	Code      string    `json:"code"`
	Enabled   bool      `json:"enabled"`
	Public    bool      `json:"public"`
	CronSecs  int       `json:"cron_secs"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

func scanDraft(row pgx.Row) (*FunctionDraft, error) {
	var d FunctionDraft
	err := row.Scan(&d.Code, &d.Enabled, &d.Public, &d.CronSecs, &d.Revision, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

func (r Functions) Draft(ctx context.Context, gameID, name string) (*FunctionDraft, error) {
	return scanDraft(r.DB.QueryRow(ctx, `SELECT d.code,d.enabled,d.public,d.cron_secs,d.revision,d.updated_at
 FROM cloud_function_drafts d JOIN cloud_functions f ON f.id=d.function_id
 WHERE f.game_id=$1 AND f.name=$2`, gameID, name))
}

func (r Functions) SaveDraft(ctx context.Context, gameID, name string, d FunctionDraft, expected int64) (*FunctionDraft, error) {
	if expected > 0 {
		out, err := scanDraft(r.DB.QueryRow(ctx, `UPDATE cloud_function_drafts d SET code=$3,enabled=$4,public=$5,cron_secs=$6,
   revision=nextval('cloud_function_revision_seq'),updated_at=now() FROM cloud_functions f
   WHERE f.id=d.function_id AND f.game_id=$1 AND f.name=$2 AND d.revision=$7
   RETURNING d.code,d.enabled,d.public,d.cron_secs,d.revision,d.updated_at`, gameID, name, d.Code, d.Enabled, d.Public, d.CronSecs, expected))
		if errors.Is(err, ErrNotFound) {
			return nil, ErrFunctionConflict
		}
		return out, err
	}
	out, err := scanDraft(r.DB.QueryRow(ctx, `INSERT INTO cloud_function_drafts(function_id,code,enabled,public,cron_secs)
 SELECT id,$3,$4,$5,$6 FROM cloud_functions WHERE game_id=$1 AND name=$2
 AND ($7::bigint=0 OR EXISTS(SELECT 1 FROM cloud_function_drafts d WHERE d.function_id=cloud_functions.id AND d.revision=$7))
 ON CONFLICT(function_id) DO UPDATE SET code=EXCLUDED.code,enabled=EXCLUDED.enabled,
 public=EXCLUDED.public,cron_secs=EXCLUDED.cron_secs,revision=nextval('cloud_function_revision_seq'),updated_at=now()
 WHERE cloud_function_drafts.revision=$7
 RETURNING code,enabled,public,cron_secs,revision,updated_at`, gameID, name, d.Code, d.Enabled, d.Public, d.CronSecs, expected))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrFunctionConflict
	}
	return out, err
}

func (r Functions) DeleteDraft(ctx context.Context, gameID, name string, expected int64) error {
	tag, err := r.DB.Exec(ctx, `DELETE FROM cloud_function_drafts d USING cloud_functions f
 WHERE d.function_id=f.id AND f.game_id=$1 AND f.name=$2 AND d.revision=$3`, gameID, name, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFunctionConflict
	}
	return nil
}

// PublishDraft atomically publishes the exact reviewed draft and removes it.
// Both draft and live revisions are required; another editor cannot be erased.
func (r Functions) PublishDraft(ctx context.Context, gameID, name string, expectedLive, expectedDraft int64) (*CloudFunction, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM cloud_functions WHERE game_id=$1 AND name=$2 AND revision=$3 FOR UPDATE`, gameID, name, expectedLive).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFunctionConflict
	}
	if err != nil {
		return nil, err
	}
	d, err := scanDraft(tx.QueryRow(ctx, `SELECT code,enabled,public,cron_secs,revision,updated_at FROM cloud_function_drafts
 WHERE function_id=$1 AND revision=$2 FOR UPDATE`, id, expectedDraft))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrFunctionConflict
	}
	if err != nil {
		return nil, err
	}
	f, err := scanFn(tx.QueryRow(ctx, `UPDATE cloud_functions SET code=$2,enabled=$3,public=$4,cron_secs=$5
 WHERE id=$1 RETURNING `+fnCols, id, d.Code, d.Enabled, d.Public, d.CronSecs))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM cloud_function_drafts WHERE function_id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return f, nil
}
