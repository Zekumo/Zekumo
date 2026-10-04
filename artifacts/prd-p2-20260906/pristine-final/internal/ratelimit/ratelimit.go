// Package ratelimit throttles abusable endpoints — login, anonymous cloud
// functions, client log reporting — using Redis counters so limits hold
// across restarts and are shared by every process.
package ratelimit

import (
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"minicloud/internal/httpx"
)

// Limiter counts events per fixed window. Fixed windows allow a burst at a
// window edge; that is an acceptable trade for one round trip per check.
type Limiter struct {
	RDB *redis.Client

	// TrustProxy makes ClientIP believe X-Forwarded-For. Only enable it
	// behind a proxy that overwrites the header — otherwise any caller can
	// forge an identity and bypass every per-IP limit.
	TrustProxy bool
}

type Rule struct {
	Name   string
	Limit  int
	Window time.Duration
}

// Allow consumes one unit for id under rule. On a Redis failure it allows the
// request: a limiter outage must not become a platform outage.
func (l Limiter) Allow(ctx context.Context, rule Rule, id string) (ok bool, retryAfter time.Duration) {
	window := int64(rule.Window / time.Second)
	if window <= 0 {
		window = 1
	}
	slot := time.Now().Unix() / window
	key := "rl:" + rule.Name + ":" + id + ":" + strconv.FormatInt(slot, 10)

	pipe := l.RDB.Pipeline()
	count := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rule.Window)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("ratelimit: %s check failed, allowing: %v", rule.Name, err)
		return true, 0
	}
	if count.Val() <= int64(rule.Limit) {
		return true, 0
	}
	elapsed := time.Now().Unix() - slot*window
	return false, time.Duration(window-elapsed) * time.Second
}

// Reset clears the current window for id, e.g. after a successful login.
func (l Limiter) Reset(ctx context.Context, rule Rule, id string) {
	window := int64(rule.Window / time.Second)
	if window <= 0 {
		window = 1
	}
	slot := time.Now().Unix() / window
	l.RDB.Del(ctx, "rl:"+rule.Name+":"+id+":"+strconv.FormatInt(slot, 10))
}

// ClientIP identifies the caller for per-IP limits.
func (l Limiter) ClientIP(r *http.Request) string {
	if l.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first, _, _ := strings.Cut(fwd, ",") // left-most entry is the client
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Middleware rejects requests over the limit with 429. keyFn decides what is
// being limited — the caller's IP, the authenticated player, and so on.
func (l Limiter) Middleware(rule Rule, keyFn func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, retryAfter := l.Allow(r.Context(), rule, keyFn(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			httpx.Error(w, http.StatusTooManyRequests, "rate_limited",
				"too many requests, slow down and retry shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ByIP is Middleware keyed on the caller's address.
func (l Limiter) ByIP(rule Rule, next http.Handler) http.Handler {
	return l.Middleware(rule, l.ClientIP, next)
}
