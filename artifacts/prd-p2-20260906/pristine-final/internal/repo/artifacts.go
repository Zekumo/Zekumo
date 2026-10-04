package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Artifacts struct{ DB *pgxpool.Pool }

const artifactCols = `id, release_id, platform, arch, filename, size, storage_key, sha256, status, created_at`

func scanArtifact(row pgx.Row) (*Artifact, error) {
	var a Artifact
	err := row.Scan(&a.ID, &a.ReleaseID, &a.Platform, &a.Arch, &a.Filename, &a.Size,
		&a.StorageKey, &a.SHA256, &a.Status, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// Create registers a pending artifact, replacing a previous upload for the
// same platform/arch slot of a draft release.
func (a Artifacts) Create(ctx context.Context, art *Artifact) error {
	return a.DB.QueryRow(ctx,
		`INSERT INTO artifacts (release_id, platform, arch, filename, size, storage_key, sha256)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (release_id, platform, arch) DO UPDATE
		   SET filename = EXCLUDED.filename, size = EXCLUDED.size, storage_key = EXCLUDED.storage_key,
		       sha256 = EXCLUDED.sha256, status = 'pending', created_at = now()
		 RETURNING id, status, created_at`,
		art.ReleaseID, art.Platform, art.Arch, art.Filename, art.Size, art.StorageKey, art.SHA256).
		Scan(&art.ID, &art.Status, &art.CreatedAt)
}

func (a Artifacts) ByID(ctx context.Context, id int64) (*Artifact, error) {
	return scanArtifact(a.DB.QueryRow(ctx,
		`SELECT `+artifactCols+` FROM artifacts WHERE id = $1`, id))
}

func (a Artifacts) ByRelease(ctx context.Context, releaseID int64) ([]Artifact, error) {
	rows, err := a.DB.Query(ctx,
		`SELECT `+artifactCols+` FROM artifacts WHERE release_id = $1 ORDER BY id`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := []Artifact{}
	for rows.Next() {
		art, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, *art)
	}
	return artifacts, rows.Err()
}

func (a Artifacts) MarkReady(ctx context.Context, id int64) error {
	tag, err := a.DB.Exec(ctx, `UPDATE artifacts SET status = 'ready' WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (a Artifacts) CountReady(ctx context.Context, releaseID int64) (int64, error) {
	var n int64
	err := a.DB.QueryRow(ctx,
		`SELECT count(*) FROM artifacts WHERE release_id = $1 AND status = 'ready'`, releaseID).Scan(&n)
	return n, err
}
