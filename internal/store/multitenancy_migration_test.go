package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isolatedPostgres(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "tenant_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return pool, ctx
}

func TestMultitenancyMigrationPreservesLegacyRows(t *testing.T) {
	pool, ctx := isolatedPostgres(t)
	if _, err := pool.Exec(ctx,
		`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		name := strings.TrimPrefix(file, "migrations/")
		if name >= "0020_" {
			continue
		}
		body, err := migrationFS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyOne(ctx, pool, name, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	var accountID, gameID, playerID, oauthID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (username,password_hash,nickname) VALUES ('legacy-user','hash','Legacy') RETURNING id`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO games (app_id,app_secret,name) VALUES ('zk_legacy','keep-secret','Legacy game') RETURNING id`).Scan(&gameID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO players (game_id,provider,identifier,nickname,account_id) VALUES ($1,'guest','device','Player',$2) RETURNING id`,
		gameID, accountID).Scan(&playerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO oauth_clients (client_id,client_secret,name) VALUES ('oc_legacy','oauth-secret','Legacy client') RETURNING id`).Scan(&oauthID); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	const legacyWorkspace = "00000000-0000-4000-8000-000000000002"
	var gotWorkspace, appID, appSecret, gotGameID, gotAccountID string
	if err := pool.QueryRow(ctx,
		`SELECT workspace_id,app_id,app_secret FROM games WHERE id=$1`, gameID,
	).Scan(&gotWorkspace, &appID, &appSecret); err != nil {
		t.Fatal(err)
	}
	if gotWorkspace != legacyWorkspace || appID != "zk_legacy" || appSecret != "keep-secret" {
		t.Fatalf("legacy game changed: workspace=%s app=%s secret=%s", gotWorkspace, appID, appSecret)
	}
	if err := pool.QueryRow(ctx,
		`SELECT game_id,account_id FROM players WHERE id=$1`, playerID,
	).Scan(&gotGameID, &gotAccountID); err != nil {
		t.Fatal(err)
	}
	if gotGameID != gameID || gotAccountID != accountID {
		t.Fatalf("legacy player changed: game=%s account=%s", gotGameID, gotAccountID)
	}
	if err := pool.QueryRow(ctx,
		`SELECT workspace_id FROM oauth_clients WHERE id=$1`, oauthID,
	).Scan(&gotWorkspace); err != nil {
		t.Fatal(err)
	}
	if gotWorkspace != legacyWorkspace {
		t.Fatalf("legacy oauth workspace=%s", gotWorkspace)
	}
	var mixedVersionWorkspace string
	if err := pool.QueryRow(ctx,
		`INSERT INTO games (app_id,app_secret,name) VALUES ('zk_old_writer','secret','Old writer') RETURNING workspace_id`,
	).Scan(&mixedVersionWorkspace); err != nil {
		t.Fatalf("mixed-version game insert failed: %v", err)
	}
	if mixedVersionWorkspace != legacyWorkspace {
		t.Fatalf("mixed-version game workspace=%s", mixedVersionWorkspace)
	}

	var games, players, accounts, clients int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM games), (SELECT count(*) FROM players),
		        (SELECT count(*) FROM accounts), (SELECT count(*) FROM oauth_clients)`,
	).Scan(&games, &players, &accounts, &clients); err != nil {
		t.Fatal(err)
	}
	if games != 2 || players != 1 || accounts != 1 || clients != 1 {
		t.Fatal(fmt.Sprintf("row counts changed: games=%d players=%d accounts=%d clients=%d", games, players, accounts, clients))
	}
}
