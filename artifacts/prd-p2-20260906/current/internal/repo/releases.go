package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrConflict = errors.New("already exists")

type Releases struct{ DB *pgxpool.Pool }

const releaseCols = `id, game_id, channel, version, changelog, status, mandatory,
	min_supported_version, rollout_percent, published_at, created_at`

func scanRelease(row pgx.Row) (*Release, error) {
	var rel Release
	err := row.Scan(&rel.ID, &rel.GameID, &rel.Channel, &rel.Version, &rel.Changelog, &rel.Status,
		&rel.Mandatory, &rel.MinSupportedVersion, &rel.RolloutPercent, &rel.PublishedAt, &rel.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rel, nil
}

func (r Releases) Create(ctx context.Context, rel *Release) error {
	err := r.DB.QueryRow(ctx,
		`INSERT INTO releases (game_id, channel, version, changelog, mandatory, min_supported_version)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, status, rollout_percent, created_at`,
		rel.GameID, rel.Channel, rel.Version, rel.Changelog, rel.Mandatory, rel.MinSupportedVersion).
		Scan(&rel.ID, &rel.Status, &rel.RolloutPercent, &rel.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func (r Releases) ByVersion(ctx context.Context, gameID, channel, version string) (*Release, error) {
	return scanRelease(r.DB.QueryRow(ctx,
		`SELECT `+releaseCols+` FROM releases WHERE game_id = $1 AND channel = $2 AND version = $3`,
		gameID, channel, version))
}

// Published returns all published releases of a game+channel; semver ordering
// happens in the updates package.
func (r Releases) Published(ctx context.Context, gameID, channel string) ([]Release, error) {
	return r.list(ctx,
		`SELECT `+releaseCols+` FROM releases WHERE game_id = $1 AND channel = $2 AND status = 'published'`,
		gameID, channel)
}

// ListByGame returns every release of a game; with publicOnly only
// published/deprecated ones (what end users may see).
func (r Releases) ListByGame(ctx context.Context, gameID string, publicOnly bool) ([]Release, error) {
	q := `SELECT ` + releaseCols + ` FROM releases WHERE game_id = $1`
	if publicOnly {
		q += ` AND status IN ('published', 'deprecated')`
	}
	return r.list(ctx, q+` ORDER BY id DESC`, gameID)
}

func (r Releases) list(ctx context.Context, query string, args ...any) ([]Release, error) {
	rows, err := r.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	releases := []Release{}
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		releases = append(releases, *rel)
	}
	return releases, rows.Err()
}

// SetStatus transitions a release, enforcing the allowed lifecycle states in
// the WHERE clause; returns ErrNotFound when the transition does not apply.
func (r Releases) SetStatus(ctx context.Context, releaseID int64, from []string, to string) error {
	var publishedAt *time.Time
	if to == "published" {
		now := time.Now().UTC()
		publishedAt = &now
	}
	tag, err := r.DB.Exec(ctx,
		`UPDATE releases SET status = $2, published_at = COALESCE($3, published_at)
		 WHERE id = $1 AND status = ANY($4)`,
		releaseID, to, publishedAt, from)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r Releases) SetRollout(ctx context.Context, releaseID int64, percent int) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE releases SET rollout_percent = $2 WHERE id = $1 AND status = 'published'`,
		releaseID, percent)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r Releases) DeleteDraft(ctx context.Context, releaseID int64) error {
	tag, err := r.DB.Exec(ctx, `DELETE FROM releases WHERE id = $1 AND status = 'draft'`, releaseID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordCheck bumps the daily update-check counters; best-effort.
func (r Releases) RecordCheck(ctx context.Context, gameID, channel, fromVersion string, updated bool) error {
	upd := 0
	if updated {
		upd = 1
	}
	_, err := r.DB.Exec(ctx,
		`INSERT INTO update_checks_daily (game_id, date, channel, from_version, checks, updates)
		 VALUES ($1, CURRENT_DATE, $2, $3, 1, $4)
		 ON CONFLICT (game_id, date, channel, from_version)
		 DO UPDATE SET checks = update_checks_daily.checks + 1,
		               updates = update_checks_daily.updates + $4`,
		gameID, channel, fromVersion, upd)
	return err
}
