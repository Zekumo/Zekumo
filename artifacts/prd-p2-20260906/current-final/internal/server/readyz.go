package server

import (
	"context"
	"net/http"
	"time"

	"minicloud/internal/httpx"
)

// Version is stamped at build time:
//
//	go build -ldflags "-X minicloud/internal/server.Version=$(git describe --tags)"
//
// It surfaces in /readyz and the console's platform status, so an operator can
// tell which build a given instance is actually running.
var Version = "dev"

const readyTimeout = 3 * time.Second

// readyz answers whether this instance can serve traffic. An instance whose
// database or cache is unreachable should be pulled out of rotation rather
// than left in it returning errors.
func (d *deps) readyz(w http.ResponseWriter, r *http.Request) {
	// Bound the probe: a hung dependency must not hold the connection open
	// longer than the caller's own patience.
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	checks := map[string]string{}
	ready := true
	if err := d.store.DB.Ping(ctx); err != nil {
		checks["postgres"], ready = err.Error(), false
	} else {
		checks["postgres"] = "ok"
	}
	if err := d.store.RDB.Ping(ctx).Err(); err != nil {
		checks["redis"], ready = err.Error(), false
	} else {
		checks["redis"] = "ok"
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	httpx.JSON(w, status, map[string]any{
		"ready":   ready,
		"version": Version,
		"checks":  checks,
	})
}
