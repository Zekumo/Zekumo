package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
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
	// Multiple application instances can start at the same time. Keep every
	// migration operation on one acquired session and serialize that session
	// with a Postgres advisory lock; CREATE IF NOT EXISTS alone is not safe
	// against concurrent catalog writes.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('minicloud:migrations'))`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock(hashtext('minicloud:migrations'))`)
	}()

	_, err = conn.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
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
		if err := applyOne(ctx, conn, name, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		log.Printf("store: applied migration %s", name)
	}
	return nil
}

type migrationDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Begin(context.Context) (pgx.Tx, error)
}

func appliedVersions(ctx context.Context, db migrationDB) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT version FROM schema_migrations`)
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
func applyOne(ctx context.Context, db migrationDB, name, body string) error {
	tx, err := db.Begin(ctx)
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
