package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies every migration the database has not seen yet, in filename
// order, each in its own transaction and recorded in schema_migrations.
//
// Replaying one big schema file on every boot only works while every
// statement is idempotent; it cannot express a data backfill, a column
// rename, or anything that must run exactly once. Tracking versions makes
// those possible and makes it obvious what a given database has applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, file := range files {
		name := file[len("migrations/"):]
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile(file)
		if err != nil {
			return err
		}
		if err := applyOne(ctx, pool, name, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		log.Printf("store: applied migration %s", name)
	}
	return nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// applyOne runs a migration and records it atomically: a failure halfway
// through leaves neither the schema change nor the version marker behind.
func applyOne(ctx context.Context, pool *pgxpool.Pool, name, body string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(ctx, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
