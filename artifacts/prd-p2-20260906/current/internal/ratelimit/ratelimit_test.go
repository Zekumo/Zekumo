package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newTestLimiter connects to the development Redis, skipping when absent so
// the suite still runs on a bare checkout. Keys are randomized per test so
// runs never interfere.
func newTestLimiter(t *testing.T) (Limiter, string) {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no Redis at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return Limiter{RDB: rdb}, hex.EncodeToString(b)
}

func TestAllowStopsAtLimit(t *testing.T) {
	l, id := newTestLimiter(t)
	rule := Rule{Name: "test-" + id, Limit: 3, Window: time.Minute}
	ctx := context.Background()

	for i := range 3 {
		if ok, _ := l.Allow(ctx, rule, id); !ok {
			t.Fatalf("request %d rejected while under the limit", i+1)
		}
	}
	ok, retryAfter := l.Allow(ctx, rule, id)
	if ok {
		t.Fatal("the 4th request was allowed past a limit of 3")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %v, want between 0 and the window", retryAfter)
	}

	// A different id has its own budget.
	if ok, _ := l.Allow(ctx, rule, id+"-other"); !ok {
		t.Error("a separate id must not inherit an exhausted budget")
	}

	l.Reset(ctx, rule, id)
	if ok, _ := l.Allow(ctx, rule, id); !ok {
		t.Error("Reset must clear the current window")
	}
}

func TestMiddlewareReturns429(t *testing.T) {
	l, id := newTestLimiter(t)
	rule := Rule{Name: "test-mw-" + id, Limit: 1, Window: time.Minute}

	served := 0
	h := l.Middleware(rule, func(*http.Request) string { return id },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served++ }))

	for range 2 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
	if served != 1 {
		t.Errorf("handler ran %d times, want 1", served)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After")
	}
}

func TestClientIPIgnoresForwardedHeaderUnlessTrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	if got := (Limiter{}).ClientIP(r); got != "203.0.113.9" {
		t.Errorf("untrusted ClientIP = %q, want the socket address", got)
	}
	if got := (Limiter{TrustProxy: true}).ClientIP(r); got != "1.2.3.4" {
		t.Errorf("trusted ClientIP = %q, want the left-most forwarded entry", got)
	}
}

func TestAttemptsLockAndClear(t *testing.T) {
	l, id := newTestLimiter(t)
	a := Attempts{RDB: l.RDB, Max: 3, Window: time.Minute}
	ctx := context.Background()
	identity := "acct-" + id

	if a.Locked(ctx, identity) {
		t.Fatal("a fresh identity must not be locked")
	}
	for i := range 2 {
		if a.Fail(ctx, identity) {
			t.Fatalf("locked after %d failures, want 3", i+1)
		}
	}
	if !a.Fail(ctx, identity) {
		t.Fatal("the 3rd failure must lock the identity")
	}
	if !a.Locked(ctx, identity) {
		t.Fatal("Locked must report the lockout")
	}
	a.Succeed(ctx, identity)
	if a.Locked(ctx, identity) {
		t.Error("a successful login must clear the lockout")
	}
}
