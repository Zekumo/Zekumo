package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"zekumo/internal/auth"
	"zekumo/internal/repo"
	"zekumo/internal/store"
)

func saveFixture(t *testing.T) (*pgxpool.Pool, context.Context, *repo.Player, *repo.Player) {
	t.Helper()
	endpoint := os.Getenv("TEST_DATABASE_URL")
	if endpoint == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	schema := "save_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	if err = store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var players [2]*repo.Player
	for i := range players {
		game, err := (repo.Games{DB: db}).Create(ctx, fmt.Sprintf("save-game-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		players[i], err = (repo.Players{DB: db}).Create(ctx, game.ID, "guest", fmt.Sprintf("device-%d", i), "", "Player")
		if err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx, players[0], players[1]
}

func TestCloudSaveConcurrentConditionalWrites(t *testing.T) {
	db, ctx, player, other := saveFixture(t)
	data := repo.PlayerData{DB: db}
	initial, err := data.Put(ctx, player.ID, "save", json.RawMessage(`{"level":1}`))
	if err != nil {
		t.Fatal(err)
	}
	const writers = 16
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := data.PutConditional(ctx, player.ID, "save", json.RawMessage(fmt.Sprintf(`{"level":%d}`, i+2)), repo.SaveCondition{Revision: initial.Revision})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, repo.ErrSaveConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != writers-1 {
		t.Fatalf("winners=%d conflicts=%d", succeeded, conflicted)
	}
	current, err := data.Get(ctx, player.ID, "save")
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision == initial.Revision {
		t.Fatal("revision did not rotate")
	}
	if err = data.DeleteConditional(ctx, player.ID, "save", initial.Revision); !errors.Is(err, repo.ErrSaveConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	if _, err = data.PutConditional(ctx, other.ID, "save", json.RawMessage(`true`), repo.SaveCondition{Revision: current.Revision}); !errors.Is(err, repo.ErrSaveConflict) {
		t.Fatalf("other player's revision accepted: %v", err)
	}
	if err = data.DeleteConditional(ctx, player.ID, "save", current.Revision); err != nil {
		t.Fatal(err)
	}
	recreated, err := data.Put(ctx, player.ID, "save", json.RawMessage(`{"level":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if recreated.Revision == initial.Revision || recreated.Revision == current.Revision {
		t.Fatal("revision reused after delete/recreate")
	}
	if _, err = data.PutConditional(ctx, player.ID, "save", json.RawMessage(`false`), repo.SaveCondition{Revision: current.Revision}); !errors.Is(err, repo.ErrSaveConflict) {
		t.Fatalf("ABA write accepted: %v", err)
	}
	if _, err = db.Exec(ctx, `UPDATE player_data SET value='{"legacy":true}' WHERE player_id=$1 AND key='save'`, player.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := data.Get(ctx, player.ID, "save")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Revision == recreated.Revision {
		t.Fatal("old-style SQL update did not rotate revision")
	}
	entries, err := data.List(ctx, player.ID)
	if err != nil || len(entries) != 1 || len(entries[0].Value) != 0 || entries[0].Revision != legacy.Revision {
		t.Fatalf("invalid list: %+v %v", entries, err)
	}
}

func TestCloudSaveConcurrentCreateAndLegacyWrites(t *testing.T) {
	db, ctx, player, _ := saveFixture(t)
	data := repo.PlayerData{DB: db}
	const writers = 12
	start := make(chan struct{})
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() {
			<-start
			_, err := data.PutConditional(ctx, player.ID, "new", json.RawMessage(`null`), repo.SaveCondition{OnlyIfAbsent: true})
			results <- err
		}()
	}
	close(start)
	wins := 0
	for i := 0; i < writers; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, repo.ErrSaveConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("create winners=%d", wins)
	}
	first, err := data.Get(ctx, player.ID, "new")
	if err != nil {
		t.Fatal(err)
	}
	second, err := data.Put(ctx, player.ID, "new", json.RawMessage(`null`))
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == second.Revision {
		t.Fatal("unconditional same-value write did not rotate revision")
	}
	if _, err = data.PutConditional(ctx, player.ID, "missing", json.RawMessage(`true`), repo.SaveCondition{Revision: "*"}); !errors.Is(err, repo.ErrSaveConflict) {
		t.Fatalf("exists-only created missing slot: %v", err)
	}
	if err = data.DeleteConditional(ctx, player.ID, "missing", "*"); !errors.Is(err, repo.ErrSaveConflict) {
		t.Fatalf("conditional missing delete=%v", err)
	}
	if err = data.Delete(ctx, player.ID, "missing"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("legacy missing delete=%v", err)
	}
}

func TestCloudSaveHTTPPreconditions(t *testing.T) {
	db, _, player, other := saveFixture(t)
	h := &Handler{Data: repo.PlayerData{DB: db}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/player/data/{key}", h.GetData)
	mux.HandleFunc("PUT /v1/player/data/{key}", h.PutData)
	mux.HandleFunc("DELETE /v1/player/data/{key}", h.DeleteData)
	issuer := auth.NewTokenIssuer("synthetic-cloud-save-tests-secret", time.Hour)
	handler := (&auth.Handler{Issuer: issuer}).Middleware(auth.RolePlayer, mux)
	token, _ := issuer.IssuePlayer(player.ID, player.GameID, "Player")
	otherToken, _ := issuer.IssuePlayer(other.ID, other.GameID, "Other")
	request := func(method, body, match, absent, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/player/data/main", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if absent != "" {
			r.Header.Set("If-None-Match", absent)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := request("PUT", `{"level":1}`, "", "*", token)
	if created.Code != 200 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	etag := created.Header().Get("ETag")
	if len(etag) != 38 {
		t.Fatalf("ETag=%q", etag)
	}
	duplicate := request("PUT", `{"level":99}`, "", "*", token)
	if duplicate.Code != 412 {
		t.Fatalf("duplicate=%d", duplicate.Code)
	}
	updated := request("PUT", `{"level":2}`, etag, "", token)
	if updated.Code != 200 || updated.Header().Get("ETag") == etag {
		t.Fatalf("update %d %s", updated.Code, updated.Body.String())
	}
	stale := request("PUT", `{"level":100}`, etag, "", token)
	if stale.Code != 412 || !strings.Contains(stale.Body.String(), "save_conflict") {
		t.Fatalf("stale=%d %s", stale.Code, stale.Body.String())
	}
	foreign := request("PUT", `{"level":100}`, updated.Header().Get("ETag"), "", otherToken)
	if foreign.Code != 412 {
		t.Fatalf("foreign revision=%d", foreign.Code)
	}
	for _, bad := range []string{`W/` + etag, etag + "," + etag, `"bad"`} {
		w := request("PUT", `{"level":100}`, bad, "", token)
		if w.Code != 400 {
			t.Fatalf("bad header accepted: %s => %d", bad, w.Code)
		}
	}
	current := request("GET", "", "", "", token)
	var entry repo.DataEntry
	if err := json.Unmarshal(current.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Level int `json:"level"`
	}
	_ = json.Unmarshal(entry.Value, &value)
	if current.Code != 200 || value.Level != 2 || current.Header().Get("ETag") != `"`+entry.Revision+`"` {
		t.Fatalf("read=%d %+v", current.Code, entry)
	}
	if got := request("DELETE", "", etag, "", token); got.Code != 412 {
		t.Fatalf("stale delete=%d", got.Code)
	}
	if got := request("DELETE", "", current.Header().Get("ETag"), "", token); got.Code != 200 {
		t.Fatalf("delete=%d", got.Code)
	}
	if got := request("GET", "", "", "", token); got.Code != 404 {
		t.Fatalf("deleted read=%d", got.Code)
	}
}
