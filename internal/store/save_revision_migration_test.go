package store

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
)

func TestSaveRevisionMigrationPreservesExistingData(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	if _, err := db.Exec(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		name := strings.TrimPrefix(file, "migrations/")
		if name >= "0021_" {
			continue
		}
		body, err := migrationFS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err = applyOne(ctx, db, name, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	var gameID, playerID string
	if err = db.QueryRow(ctx, `INSERT INTO games(app_id,app_secret,name) VALUES('test','test','test') RETURNING id`).Scan(&gameID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO players(game_id,provider,identifier) VALUES($1,'guest','test') RETURNING id`, gameID).Scan(&playerID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO player_data(player_id,key,value,updated_at) VALUES($1,'main','{"level":8}','2026-01-01T00:00:00Z'),($1,'other','null','2026-01-01T00:00:00Z')`, playerID); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err = db.QueryRow(ctx, `SELECT count(*)=2 AND count(DISTINCT revision)=2 AND bool_and(updated_at='2026-01-01T00:00:00Z') FROM player_data`).Scan(&valid); err != nil || !valid {
		t.Fatalf("backfill changed rows: %v %v", valid, err)
	}
	var first, second, value string
	if err = db.QueryRow(ctx, `SELECT revision::text,value::text FROM player_data WHERE key='main'`).Scan(&first, &value); err != nil || value != `{"level": 8}` {
		t.Fatalf("value changed: %q %v", value, err)
	}
	// Simulate an old app instance writing without knowledge of revisions.
	if err = db.QueryRow(ctx, `UPDATE player_data SET value='{"level":9}' WHERE key='main' RETURNING revision::text`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("legacy writer did not rotate revision")
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = db.QueryRow(ctx, `SELECT revision::text FROM player_data WHERE key='main'`).Scan(&after); err != nil || after != second {
		t.Fatalf("migration replay changed revision: %s %v", after, err)
	}
}
