package leaderboard

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// legacyFriends is the previous full-board filtering algorithm. Retained only
// in tests as the result oracle and as a reproducible performance baseline.
func legacyFriends(zs []redis.Z, allowed map[string]struct{}) []Entry {
	all := make([]Entry, 0, len(allowed))
	for _, z := range zs {
		id, ok := z.Member.(string)
		if !ok {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		all = append(all, Entry{PlayerID: id, Score: int64(z.Score)})
	}
	return all
}

func fixture(size int) ([]redis.Z, map[string]struct{}, []string) {
	zs := make([]redis.Z, size)
	allowed := make(map[string]struct{})
	ids := make([]string, 0, 101)
	for i := range size {
		id := fmt.Sprintf("player-%08d", i)
		zs[i] = redis.Z{Member: id, Score: float64(i % 257)}
		if i < 101 {
			allowed[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Slice(zs, func(i, j int) bool {
		if zs[i].Score != zs[j].Score {
			return zs[i].Score > zs[j].Score
		}
		return zs[i].Member.(string) > zs[j].Member.(string)
	})
	return zs, allowed, ids
}

var entriesSink []Entry

func BenchmarkFriendsFilterLegacy(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("board_%d_friends_101", n), func(b *testing.B) {
			zs, allowed, _ := fixture(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				entriesSink = legacyFriends(zs, allowed)
			}
		})
	}
}

// Redis benchmarks opt in explicitly, so they never load a configured
// application database. Use an isolated local Redis and a disposable DB.
func benchmarkRedis(b *testing.B) *redis.Client {
	addr := os.Getenv("LEADERBOARD_BENCH_REDIS_ADDR")
	if addr == "" {
		b.Skip("set LEADERBOARD_BENCH_REDIS_ADDR to an isolated local Redis")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	b.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		b.Fatal(err)
	}
	return rdb
}

func BenchmarkFriendsRedisLegacy(b *testing.B) {
	rdb := benchmarkRedis(b)
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("board_%d_friends_101", n), func(b *testing.B) {
			zs, allowed, _ := fixture(n)
			k := fmt.Sprintf("lb:benchmark:%d", time.Now().UnixNano())
			ctx := context.Background()
			b.Cleanup(func() { rdb.Del(ctx, k) })
			for i := 0; i < len(zs); i += 1000 {
				if err := rdb.ZAdd(ctx, k, zs[i:min(i+1000, len(zs))]...).Err(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := rdb.ZRevRangeWithScores(ctx, k, 0, -1).Result()
				if err != nil {
					b.Fatal(err)
				}
				entriesSink = legacyFriends(got, allowed)
			}
		})
	}
}

func TestLegacyFriendsFixture(t *testing.T) {
	zs, allowed, _ := fixture(1000)
	got := legacyFriends(zs, allowed)
	if len(got) != 101 {
		t.Fatalf("got %d entries", len(got))
	}
	want := []Entry{{PlayerID: "player-00000100", Score: 100}, {PlayerID: "player-00000099", Score: 99}}
	if !reflect.DeepEqual(got[:2], want) {
		t.Fatalf("unexpected first entries: %v", got[:2])
	}
}

func TestFriendEntriesPreserveOrderingAndMissingScores(t *testing.T) {
	ids := []string{"missing", "negative", "zero", "tie-a", "tie-z", "fraction-a", "fraction-z"}
	scores := []any{nil, "-3", "0", "42", float64(42), "1.9", float64(1.1)}
	got, err := friendEntries(ids, scores)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{PlayerID: "tie-z", Score: 42}, {PlayerID: "tie-a", Score: 42}, {PlayerID: "fraction-a", Score: 1}, {PlayerID: "fraction-z", Score: 1}, {PlayerID: "zero", Score: 0}, {PlayerID: "negative", Score: -3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	for _, tc := range []struct {
		ids    []string
		scores []any
	}{
		{[]string{"a"}, nil}, {[]string{"a"}, []any{"invalid"}}, {[]string{"a"}, []any{true}},
	} {
		if _, err := friendEntries(tc.ids, tc.scores); err == nil {
			t.Fatal("expected invalid reply error")
		}
	}
	empty, err := friendEntries([]string{"missing"}, []any{nil})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
}

func TestFriendScoresRedisEquivalence(t *testing.T) {
	addr := os.Getenv("LEADERBOARD_BENCH_REDIS_ADDR")
	if addr == "" {
		t.Skip("set LEADERBOARD_BENCH_REDIS_ADDR to an isolated local Redis")
	}
	ctx := context.Background()
	for _, protocol := range []int{2, 3} {
		t.Run(fmt.Sprintf("RESP%d", protocol), func(t *testing.T) {
			rdb := redis.NewClient(&redis.Options{Addr: addr, Protocol: protocol})
			defer rdb.Close()
			game := fmt.Sprintf("test-friends-%d", time.Now().UnixNano())
			k := key(game, "score")
			defer rdb.Del(ctx, k, key(game+"-other", "score"))
			zs, allowed, ids := fixture(1000)
			zs = append(zs, redis.Z{Member: "negative", Score: -5}, redis.Z{Member: "fraction-a", Score: 1.9}, redis.Z{Member: "fraction-z", Score: 1.1}, redis.Z{Member: "tie-a", Score: 500}, redis.Z{Member: "tie-z", Score: 500})
			for _, id := range []string{"negative", "fraction-a", "fraction-z", "tie-a", "tie-z", "missing"} {
				allowed[id] = struct{}{}
				ids = append(ids, id)
			}
			if err := rdb.ZAdd(ctx, k, zs...).Err(); err != nil {
				t.Fatal(err)
			}
			if err := rdb.ZAdd(ctx, key(game+"-other", "score"), redis.Z{Member: ids[0], Score: 99999}).Err(); err != nil {
				t.Fatal(err)
			}
			legacy, err := rdb.ZRevRangeWithScores(ctx, k, 0, -1).Result()
			if err != nil {
				t.Fatal(err)
			}
			svc := &Service{RDB: rdb}
			got, err := svc.friendScores(ctx, game, "score", ids)
			if err != nil {
				t.Fatal(err)
			}
			if want := legacyFriends(legacy, allowed); !reflect.DeepEqual(got, want) {
				t.Fatalf("bounded results differ from full-board oracle: got %#v want %#v", got, want)
			}
			for _, tc := range []struct {
				game, board string
				ids         []string
			}{
				{game, "score", []string{"missing"}}, {game, "missing-board", ids}, {game + "-missing", "score", ids}, {game, "score", nil},
			} {
				got, err := svc.friendScores(ctx, tc.game, tc.board, tc.ids)
				if err != nil || got == nil || len(got) != 0 {
					t.Fatalf("empty result = %#v, %v", got, err)
				}
			}
		})
	}
}

func BenchmarkFriendsRedisBounded(b *testing.B) {
	rdb := benchmarkRedis(b)
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("board_%d_friends_101", n), func(b *testing.B) {
			zs, _, ids := fixture(n)
			game := fmt.Sprintf("benchmark-bounded-%d", time.Now().UnixNano())
			k := key(game, "score")
			ctx := context.Background()
			b.Cleanup(func() { rdb.Del(ctx, k) })
			for i := 0; i < len(zs); i += 1000 {
				if err := rdb.ZAdd(ctx, k, zs[i:min(i+1000, len(zs))]...).Err(); err != nil {
					b.Fatal(err)
				}
			}
			svc := &Service{RDB: rdb}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				entriesSink, err = svc.friendScores(ctx, game, "score", ids)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestFriendPageBoundaries(t *testing.T) {
	all := make([]Entry, 120)
	for i := range all {
		all[i].PlayerID = fmt.Sprint(i)
	}
	for _, tc := range []struct {
		name                         string
		offset, limit, start, length int64
	}{
		{"negative offset", -1, 10, 0, 10}, {"default limit", 0, 0, 0, 50}, {"oversized limit", 0, 101, 0, 50}, {"negative limit", 0, -1, 0, 50}, {"last page", 115, 10, 115, 5}, {"beyond end", 1000, 10, 120, 0}, {"large offset", int64(^uint64(0) >> 1), 100, 120, 0}, {"normal page", 10, 20, 10, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, start := friendPage(all, tc.offset, tc.limit)
			if start != tc.start || int64(len(page)) != tc.length {
				t.Fatalf("start=%d len=%d; want %d,%d", start, len(page), tc.start, tc.length)
			}
			if len(page) > 0 && page[0].PlayerID != fmt.Sprint(start) {
				t.Fatalf("wrong first player %s", page[0].PlayerID)
			}
		})
	}
	page, start := friendPage([]Entry{}, 0, 50)
	if start != 0 || len(page) != 0 {
		t.Fatalf("empty page=%v start=%d", page, start)
	}
}
