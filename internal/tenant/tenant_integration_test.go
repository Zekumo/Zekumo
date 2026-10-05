package tenant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"zekumo/internal/auth"
	"zekumo/internal/repo"
	storepkg "zekumo/internal/store"
)

func isolatedTenantDB(t *testing.T) (*pgxpool.Pool, context.Context) {
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
	schema := "tenant_auth_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := storepkg.Migrate(ctx, pool); err != nil {
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

func TestWorkspaceAuthorizationAndImmediateRevocation(t *testing.T) {
	db, ctx := isolatedTenantDB(t)
	tenants := Store{DB: db}
	owner, err := tenants.EnsureBootstrap(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	workspaceB, err := tenants.CreateWorkspace(ctx, owner.ID, "Studio B", "studio-b")
	if err != nil {
		t.Fatal(err)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("member-password"), bcrypt.MinCost)
	accounts := repo.Accounts{DB: db}
	account, err := accounts.Create(ctx, "viewer-account", string(hash), "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := tenants.AddAccountMember(ctx, LegacyWorkspaceID, account.ID, account.Username, "viewer")
	if err != nil {
		t.Fatal(err)
	}

	games := repo.Games{DB: db}
	gameA, err := games.CreateForWorkspace(ctx, LegacyWorkspaceID, "Game A")
	if err != nil {
		t.Fatal(err)
	}
	gameB, err := games.CreateForWorkspace(ctx, workspaceB.ID, "Game B")
	if err != nil {
		t.Fatal(err)
	}
	var playerA, exportA string
	if err := db.QueryRow(ctx,
		`INSERT INTO players (game_id,provider,identifier,nickname) VALUES ($1,'guest','fixture','Fixture') RETURNING id`,
		gameA.ID).Scan(&playerA); err != nil {
		t.Fatal(err)
	}
	var webhookA, achievementA, announcementA, currencyA, oauthA string
	if err := db.QueryRow(ctx,
		`INSERT INTO webhooks (game_id,url,secret,events) VALUES ($1,'https://example.invalid','secret','*') RETURNING id`, gameA.ID,
	).Scan(&webhookA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		`INSERT INTO achievement_defs (game_id,key,name) VALUES ($1,'fixture','Fixture') RETURNING id`, gameA.ID,
	).Scan(&achievementA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		`INSERT INTO announcements (game_id,title,body) VALUES ($1,'Fixture','Body') RETURNING id`, gameA.ID,
	).Scan(&announcementA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		`INSERT INTO currencies (game_id,name,display_name) VALUES ($1,'fixture','Fixture') RETURNING id`, gameA.ID,
	).Scan(&currencyA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		`INSERT INTO oauth_clients (workspace_id,client_id,client_secret,name) VALUES ($1,'oc_fixture','secret','Fixture') RETURNING client_id`,
		LegacyWorkspaceID).Scan(&oauthA); err != nil {
		t.Fatal(err)
	}
	var releaseID, artifactA int64
	if err := db.QueryRow(ctx,
		`INSERT INTO releases (game_id,channel,version) VALUES ($1,'stable','1.0.0') RETURNING id`, gameA.ID,
	).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		`INSERT INTO artifacts (release_id,platform,arch,filename,size,storage_key,sha256)
		 VALUES ($1,'linux','amd64','fixture.zip',1,'releases/fixture','00') RETURNING id`, releaseID,
	).Scan(&artifactA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx,
		`INSERT INTO export_jobs (game_id,scope,format) VALUES ($1,'all','json') RETURNING id`,
		gameA.ID).Scan(&exportA); err != nil {
		t.Fatal(err)
	}

	issuer := auth.NewTokenIssuer("tenant-test-secret-that-is-long-enough", time.Hour)
	authH := &auth.Handler{Issuer: issuer}
	viewerToken, err := issuer.IssueAdmin(viewer.UserID, viewer.Username)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := issuer.IssueAdmin(owner.ID, owner.Username)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("GET /games/{id}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceGame, ok)))
	mux.Handle("POST /games/{id}", authH.Middleware(auth.RoleAdmin, tenants.Require("editor", ResourceGame, ok)))
	mux.Handle("GET /players/{pid}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourcePlayer, ok)))
	mux.Handle("GET /exports/{job_id}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceExport, ok)))
	mux.Handle("GET /webhooks/{wid}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceWebhook, ok)))
	mux.Handle("GET /achievements/{aid}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceAchievement, ok)))
	mux.Handle("GET /announcements/{aid}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceAnnouncement, ok)))
	mux.Handle("GET /currencies/{cid}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceCurrency, ok)))
	mux.Handle("GET /oauth/{client_id}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceOAuthClient, ok)))
	mux.Handle("GET /games/{id}/artifacts/{aid}", authH.Middleware(auth.RoleAdmin, tenants.Require("viewer", ResourceArtifact, ok)))

	do := func(method, path, token, workspace string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if workspace != "" {
			req.Header.Set(WorkspaceHeader, workspace)
		}
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res.Code
	}
	if got := do("GET", "/games/"+gameA.ID, viewerToken, LegacyWorkspaceID); got != http.StatusNoContent {
		t.Fatalf("viewer own game: got %d", got)
	}
	if got := do("POST", "/games/"+gameA.ID, viewerToken, LegacyWorkspaceID); got != http.StatusForbidden {
		t.Fatalf("viewer write: got %d", got)
	}
	if got := do("GET", "/games/"+gameB.ID, ownerToken, LegacyWorkspaceID); got != http.StatusNotFound {
		t.Fatalf("cross-workspace game: got %d", got)
	}
	if got := do("GET", "/players/"+playerA, ownerToken, workspaceB.ID); got != http.StatusNotFound {
		t.Fatalf("cross-workspace player: got %d", got)
	}
	if got := do("GET", "/exports/"+exportA, ownerToken, workspaceB.ID); got != http.StatusNotFound {
		t.Fatalf("cross-workspace export: got %d", got)
	}
	for name, path := range map[string]string{
		"webhook":      "/webhooks/" + webhookA,
		"achievement":  "/achievements/" + achievementA,
		"announcement": "/announcements/" + announcementA,
		"currency":     "/currencies/" + currencyA,
		"oauth client": "/oauth/" + oauthA,
		"artifact":     "/games/" + gameA.ID + "/artifacts/" + strconv.FormatInt(artifactA, 10),
	} {
		if got := do("GET", path, ownerToken, workspaceB.ID); got != http.StatusNotFound {
			t.Errorf("cross-workspace %s: got %d", name, got)
		}
		if got := do("GET", path, ownerToken, LegacyWorkspaceID); got != http.StatusNoContent {
			t.Errorf("own %s: got %d", name, got)
		}
	}
	if got := do("GET", "/games/"+gameA.ID, viewerToken, ""); got != http.StatusBadRequest {
		t.Fatalf("missing workspace: got %d", got)
	}

	if err := tenants.RemoveMember(ctx, LegacyWorkspaceID, viewer.UserID, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := do("GET", "/games/"+gameA.ID, viewerToken, LegacyWorkspaceID); got != http.StatusForbidden {
		t.Fatalf("revoked token remained usable: got %d", got)
	}
	if err := tenants.RemoveMember(ctx, workspaceB.ID, owner.ID, "owner"); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner removal: got %v", err)
	}
}
