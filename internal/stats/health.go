package stats

import (
	"context"
	"net/http"
	"time"

	"zekumo/internal/httpx"
	"zekumo/internal/tenant"
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
	workspaceID := tenant.FromContext(ctx).WorkspaceID
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
	scanWorkspace(ctx, h.Svc,
		`SELECT COALESCE(sum(a.size),0) FROM artifacts a JOIN releases r ON r.id=a.release_id JOIN games g ON g.id=r.game_id
		 WHERE a.status='ready' AND g.workspace_id=$1`, workspaceID, &out.ArtifactBytes)
	scanWorkspace(ctx, h.Svc, `SELECT count(*) FROM app_logs l JOIN games g ON g.id=l.game_id WHERE g.workspace_id=$1`, workspaceID, &out.LogRows)
	scanWorkspace(ctx, h.Svc, `SELECT count(*) FROM chat_messages c JOIN games g ON g.id=c.game_id WHERE g.workspace_id=$1`, workspaceID, &out.ChatRows)
	scanWorkspace(ctx, h.Svc,
		`SELECT count(*) FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id JOIN games g ON g.id=w.game_id WHERE g.workspace_id=$1`,
		workspaceID, &out.WebhookTotal)
	scanWorkspace(ctx, h.Svc,
		`SELECT count(*) FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id JOIN games g ON g.id=w.game_id
		 WHERE d.ok AND g.workspace_id=$1`, workspaceID, &out.WebhookOK)
	scanWorkspace(ctx, h.Svc, `SELECT count(*) FROM app_logs l JOIN games g ON g.id=l.game_id
	                  WHERE g.workspace_id=$1 AND l.source='funcs' AND l.level='error' AND l.created_at > now() - interval '24 hours'`,
		workspaceID, &out.FunctionErrors24h)
	scanWorkspace(ctx, h.Svc, `SELECT count(*) FROM app_logs l JOIN games g ON g.id=l.game_id
	                  WHERE g.workspace_id=$1 AND l.source='http' AND l.level='error' AND l.created_at > now() - interval '24 hours'`,
		workspaceID, &out.HTTPErrors24h)

	httpx.JSON(w, http.StatusOK, out)
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

func scanWorkspace(ctx context.Context, s *Service, query, workspaceID string, dst *int64) {
	_ = s.DB.QueryRow(ctx, query, workspaceID).Scan(dst)
}
