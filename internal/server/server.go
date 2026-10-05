// Package server wires the platform together: newDeps builds the services
// (deps.go), routes registers the endpoints (routes.go), and New wraps the
// result in the cross-cutting middleware below.
package server

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"zekumo/internal/config"
	"zekumo/internal/httpx"
	"zekumo/internal/logs"
	"zekumo/internal/storage"
	"zekumo/internal/store"
)

// New builds the HTTP handler. ctx bounds every background worker the platform
// starts (log writer, retention, cron, stats aggregation), so they stop with
// the server instead of outliving it.
//
// The returned wait function blocks until those workers have finished their
// final work — call it after Server.Shutdown and before closing the store,
// or the last batch of buffered logs is written to a closed pool.
func New(ctx context.Context, cfg config.Config, st *store.Store, blob storage.Storage) (http.Handler, func(context.Context)) {
	d := newDeps(ctx, cfg, st, blob)
	handler := withCORS(withLogging(d.logs, cfg.LogHTTPAll, routes(d)))
	return handler, d.logs.Wait
}

// withCORS lets browser-based mini-games call the API from any origin.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Zekumo-Workspace-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// slowRequest is the latency above which a successful request is still worth
// a log row.
const slowRequest = time.Second

func withLogging(logSvc *logs.Service, logAll bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		ctx := httpx.ContextWithGameIDSlot(r.Context())
		ctx = httpx.ContextWithWorkspaceIDSlot(ctx)
		r = r.WithContext(ctx)
		next.ServeHTTP(sw, r)
		dur := time.Since(start)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, dur.Round(time.Millisecond))
		if !loggedPath(r.URL.Path) {
			return
		}
		// One row per API call would make the log table the busiest thing in
		// the database, so successful fast requests are dropped by default.
		if !logAll && sw.status < 400 && dur < slowRequest {
			return
		}
		level := "info"
		if sw.status >= 500 {
			level = "error"
		} else if sw.status >= 400 {
			level = "warn"
		}
		logSvc.Write(httpx.CtxGameID(r.Context()), level, "http", r.Method+" "+r.URL.Path,
			http.StatusText(sw.status), map[string]any{
				"status":      sw.status,
				"duration_ms": dur.Milliseconds(),
			})
	})
}

// loggedPath keeps access logs to API traffic (not static pages or health).
func loggedPath(p string) bool {
	if p == "/v1/ws" { // hijacked; duration would be connection lifetime
		return false
	}
	return strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/admin/api/") ||
		strings.HasPrefix(p, "/sso/api/") || strings.HasPrefix(p, "/oauth/")
}

// statusWriter captures the response code; Hijack passes through so the
// WebSocket upgrade keeps working under the wrapper.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	return h.Hijack()
}
