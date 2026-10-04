package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ExportJob struct {
	ID         string     `json:"id"`
	GameID     string     `json:"game_id"`
	Scope      string     `json:"scope"`
	PlayerID   *string    `json:"player_id,omitempty"`
	Format     string     `json:"format"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	StorageKey string     `json:"-"`
	Size       int64      `json:"size"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type ExportJobs struct{ DB *pgxpool.Pool }

const exportCols = `id, game_id, scope, player_id, format, status, error, storage_key, size, created_at, finished_at`

func scanExport(row pgx.Row) (*ExportJob, error) {
	var j ExportJob
	err := row.Scan(&j.ID, &j.GameID, &j.Scope, &j.PlayerID, &j.Format, &j.Status,
		&j.Error, &j.StorageKey, &j.Size, &j.CreatedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &j, err
}

func (r ExportJobs) Create(ctx context.Context, gameID, scope string, playerID *string, format string) (*ExportJob, error) {
	return scanExport(r.DB.QueryRow(ctx,
		`INSERT INTO export_jobs (game_id, scope, player_id, format)
		 VALUES ($1,$2,$3,$4) RETURNING `+exportCols,
		gameID, scope, playerID, format))
}

func (r ExportJobs) ByID(ctx context.Context, id string) (*ExportJob, error) {
	return scanExport(r.DB.QueryRow(ctx, `SELECT `+exportCols+` FROM export_jobs WHERE id=$1`, id))
}

func (r ExportJobs) ByGame(ctx context.Context, gameID string, limit, offset int) ([]ExportJob, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+exportCols+` FROM export_jobs WHERE game_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, gameID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportJob{}
	for rows.Next() {
		j, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (r ExportJobs) SetRunning(ctx context.Context, id string) error {
	_, err := r.DB.Exec(ctx, `UPDATE export_jobs SET status='running' WHERE id=$1`, id)
	return err
}

func (r ExportJobs) SetDone(ctx context.Context, id, storageKey string, size int64) error {
	_, err := r.DB.Exec(ctx,
		`UPDATE export_jobs SET status='done', storage_key=$2, size=$3, finished_at=now() WHERE id=$1`,
		id, storageKey, size)
	return err
}

func (r ExportJobs) SetFailed(ctx context.Context, id, msg string) error {
	_, err := r.DB.Exec(ctx,
		`UPDATE export_jobs SET status='failed', error=$2, finished_at=now() WHERE id=$1`, id, msg)
	return err
}

// FailStale marks jobs interrupted by a restart, so they do not show as
// running forever.
func (r ExportJobs) FailStale(ctx context.Context) error {
	_, err := r.DB.Exec(ctx,
		`UPDATE export_jobs SET status='failed', error='interrupted by server restart', finished_at=now()
		 WHERE status IN ('pending','running')`)
	return err
}
