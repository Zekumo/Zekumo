package bans

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"minicloud/internal/repo"
)

type fakeBanStore struct {
	mu     sync.Mutex
	active *repo.PlayerBan
	err    error
	calls  int
}

func (f *fakeBanStore) Active(context.Context, string) (*repo.PlayerBan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.active == nil {
		return nil, repo.ErrNotFound
	}
	return f.active, nil
}

func (f *fakeBanStore) Create(context.Context, string, string, string, string, *time.Time) (*repo.PlayerBan, error) {
	panic("unexpected Create")
}

func (f *fakeBanStore) CreateAndSet(context.Context, string, string, string, string, *time.Time) (*repo.PlayerBan, error) {
	panic("unexpected CreateAndSet")
}

func (f *fakeBanStore) Lift(context.Context, string, string) (*repo.PlayerBan, error) {
	panic("unexpected Lift")
}

func (f *fakeBanStore) LiftAndClear(context.Context, string, string) (*repo.PlayerBan, error) {
	panic("unexpected LiftAndClear")
}

func (f *fakeBanStore) ByGame(context.Context, string, int, int) ([]repo.PlayerBan, error) {
	panic("unexpected ByGame")
}

type fakeBanCache struct {
	mu     sync.Mutex
	values map[string]string
	err    error
}

func (f *fakeBanCache) Exists(_ context.Context, keys ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return redis.NewIntResult(0, f.err)
	}
	var count int64
	for _, key := range keys {
		if _, ok := f.values[key]; ok {
			count++
		}
	}
	return redis.NewIntResult(count, nil)
}

func (f *fakeBanCache) Set(_ context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return redis.NewStatusResult("", f.err)
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[key] = "set"
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeBanCache) Del(_ context.Context, keys ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return redis.NewIntResult(0, f.err)
	}
	var count int64
	for _, key := range keys {
		if _, ok := f.values[key]; ok {
			delete(f.values, key)
			count++
		}
	}
	return redis.NewIntResult(count, nil)
}

func TestBanExpiry(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	expires, ttl, err := banExpiry(now, 0)
	if err != nil || expires != nil || ttl != 0 {
		t.Fatalf("permanent ban = %v, %v, %v", expires, ttl, err)
	}
	expires, ttl, err = banExpiry(now, 90)
	if err != nil || ttl != 90*time.Second || expires == nil || !expires.Equal(now.Add(90*time.Second)) {
		t.Fatalf("timed ban = %v, %v, %v", expires, ttl, err)
	}
	for _, seconds := range []int64{-1, maxBanDurationSeconds + 1} {
		if _, _, err := banExpiry(now, seconds); err == nil {
			t.Fatalf("expected duration %d to be rejected", seconds)
		}
	}
}

func TestTokenBannedRehydratesAfterRedisFlush(t *testing.T) {
	store := &fakeBanStore{active: &repo.PlayerBan{PlayerID: "p1", Reason: "abuse"}}
	cache := &fakeBanCache{values: map[string]string{}}
	svc := &Service{Bans: store, RDB: cache}
	if !svc.TokenBanned(context.Background(), "p1") {
		t.Fatal("active database ban became valid after an empty Redis cache")
	}
	if _, ok := cache.values[banKey("p1")]; !ok {
		t.Fatal("active ban was not rehydrated into Redis")
	}
}

func TestTokenBannedNegativeCacheAndFailureMode(t *testing.T) {
	store := &fakeBanStore{}
	cache := &fakeBanCache{values: map[string]string{}}
	svc := &Service{Bans: store, RDB: cache}
	if svc.TokenBanned(context.Background(), "p1") {
		t.Fatal("unbanned player reported banned")
	}
	if _, ok := cache.values[noBanKey("p1")]; !ok {
		t.Fatal("negative lookup was not cached")
	}
	calls := store.calls
	if svc.TokenBanned(context.Background(), "p1") {
		t.Fatal("negative cache reported a ban")
	}
	if store.calls != calls {
		t.Fatal("negative cache did not avoid a repeated database lookup")
	}

	cache.err = errors.New("redis unavailable")
	store.err = errors.New("postgres unavailable")
	if !svc.TokenBanned(context.Background(), "p2") {
		t.Fatal("blacklist verification must fail closed when both stores are unavailable")
	}
}
