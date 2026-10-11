package leaderboard

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"zekumo/internal/repo"
	"zekumo/internal/store"
)

func TestTopFriendsIntegration(t *testing.T) {
	url, addr := os.Getenv("TEST_DATABASE_URL"), os.Getenv("LEADERBOARD_BENCH_REDIS_ADDR")
	if url == "" || addr == "" {
		t.Skip("requires isolated TEST_DATABASE_URL and LEADERBOARD_BENCH_REDIS_ADDR")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "leaderboard_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`)
	}()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	game, err := (repo.Games{DB: db}).Create(ctx, "Friends integration")
	if err != nil {
		t.Fatal(err)
	}
	players := repo.Players{DB: db}
	friends := repo.Friendships{DB: db}
	scores := repo.Scores{DB: db}
	ids := map[string]string{}
	for _, name := range []string{"caller", "high", "zero", "missing", "pending", "stranger"} {
		player, err := players.Create(ctx, game.ID, "guest", name, "", name+" nickname")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = player.ID
	}
	for _, name := range []string{"high", "zero", "missing"} {
		if err = friends.Add(ctx, game.ID, ids["caller"], ids[name]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = (repo.FriendRequests{DB: db}).Create(ctx, game.ID, ids["caller"], ids["pending"]); err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	defer rdb.Del(context.Background(), key(game.ID, "score"))
	svc := &Service{RDB: rdb, Scores: scores, Players: players}
	// Populate the durable scores only: the first call must also exercise ensure.
	for name, score := range map[string]int64{"caller": 20, "high": 50, "zero": 0, "pending": 99, "stranger": 1000} {
		if err = scores.Upsert(ctx, game.ID, "score", ids[name], score); err != nil {
			t.Fatal(err)
		}
	}
	want := []Entry{{Rank: 1, PlayerID: ids["high"], Nickname: "high nickname", Score: 50}, {Rank: 2, PlayerID: ids["caller"], Nickname: "caller nickname", Score: 20}, {Rank: 3, PlayerID: ids["zero"], Nickname: "zero nickname", Score: 0}}
	got, err := svc.TopFriends(ctx, game.ID, "score", ids["caller"], 0, 50, friends)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("full page got %#v want %#v", got, want)
	}
	got, err = svc.TopFriends(ctx, game.ID, "score", ids["caller"], 1, 1, friends)
	if err != nil || !reflect.DeepEqual(got, want[1:2]) {
		t.Fatalf("second page got %#v err=%v", got, err)
	}
	got, err = svc.TopFriends(ctx, game.ID, "score", ids["caller"], 99, 1, friends)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty page got %#v err=%v", got, err)
	}
	if err = friends.Remove(ctx, game.ID, ids["caller"], ids["high"]); err != nil {
		t.Fatal(err)
	}
	got, err = svc.TopFriends(ctx, game.ID, "score", ids["caller"], 0, 50, friends)
	if err != nil {
		t.Fatal(err)
	}
	want = want[1:]
	want[0].Rank = 1
	want[1].Rank = 2
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after unfriend got %#v want %#v", got, want)
	}
}
