package stats

import (
	"context"
	"net/http"
	"time"

	"zekumo/internal/httpx"
)

// Health is the platform's own condition, as opposed to game activity: are
// the dependencies responding, how much has accumulated, and are the two
// subsystems that talk to the outside world (webhooks, cloud functions)
// currently failing.
type Health struct {
	Version       string `json:"version"`
	Env           string `json:"env"`
	UptimeSeconds int64  `json:"uptime_seconds"`

	Postgres Dependency `json:"postgres"`
	Redis    Dependency `json:"redis"`

	ArtifactBytes int64 `json:"artifact_bytes"`
	LogRows       int64 `json:"log_rows"`
	ChatRows      int64 `json:"chat_rows"`

	// Webhook delivery over the retained history.
	WebhookTotal int64 `json:"webhook_total"`
	WebhookOK    int64 `json:"webhook_ok"`

	FunctionErrors24h int64 `json:"function_errors_24h"`
	HTTPErrors24h     int64 `json:"http_errors_24h"`
}

type Dependency struct {
	OK        bool    `json:"ok"`
	LatencyMS float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
}

type HealthHandler struct {
	Svc       *Service
	StartedAt time.Time
	Version   string
	Env       string
}

// Health handles GET /admin/api/health.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := Health{
		Version:       h.Version,
		Env:           h.Env,
		UptimeSeconds: int64(time.Since(h.StartedAt).Seconds()),
	}

	start := time.Now()
	if err := h.Svc.DB.Ping(ctx); err != nil {
		out.Postgres.Error = err.Error()
	} else {
		out.Postgres.OK = true
	}
	out.Postgres.LatencyMS = msSince(start)

	start = time.Now()
	if err := h.Svc.RDB.Ping(ctx).Err(); err != nil {
		out.Redis.Error = err.Error()
	} else {
		out.Redis.OK = true
	}
	out.Redis.LatencyMS = msSince(start)

	// Counters are best-effort: a dashboard should still render if one of
	// these queries fails rather than returning nothing at all.
	scan(ctx, h.Svc, `SELECT COALESCE(sum(size), 0) FROM artifacts WHERE status = 'ready'`, &out.ArtifactBytes)
	scan(ctx, h.Svc, `SELECT count(*) FROM app_logs`, &out.LogRows)
	scan(ctx, h.Svc, `SELECT count(*) FROM chat_messages`, &out.ChatRows)
	scan(ctx, h.Svc, `SELECT count(*) FROM webhook_deliveries`, &out.WebhookTotal)
	scan(ctx, h.Svc, `SELECT count(*) FROM webhook_deliveries WHERE ok`, &out.WebhookOK)
	scan(ctx, h.Svc, `SELECT count(*) FROM app_logs
	                  WHERE source = 'funcs' AND level = 'error' AND created_at > now() - interval '24 hours'`,
		&out.FunctionErrors24h)
	scan(ctx, h.Svc, `SELECT count(*) FROM app_logs
	                  WHERE source = 'http' AND level = 'error' AND created_at > now() - interval '24 hours'`,
		&out.HTTPErrors24h)

	httpx.JSON(w, http.StatusOK, out)
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

func scan(ctx context.Context, s *Service, query string, dst *int64) {
	_ = s.DB.QueryRow(ctx, query).Scan(dst)
}
