package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zekumo/internal/repo"
)

func TestViewerSecretRedaction(t *testing.T) {
	games := []repo.Game{{AppSecret: "app-secret"}}
	clients := []repo.OAuthClient{{ClientSecret: "oauth-secret"}}
	redactGameSecrets(games, "viewer")
	redactOAuthSecrets(clients, "viewer")
	if games[0].AppSecret != "" || clients[0].ClientSecret != "" {
		t.Fatal("viewer response retained a secret")
	}
	games[0].AppSecret = "app-secret"
	clients[0].ClientSecret = "oauth-secret"
	redactGameSecrets(games, "editor")
	redactOAuthSecrets(clients, "editor")
	if games[0].AppSecret == "" || clients[0].ClientSecret == "" {
		t.Fatal("editor response unexpectedly lost a secret")
	}
}

type lockedLimiter struct{}

func (lockedLimiter) Fail(context.Context, string) bool   { return true }
func (lockedLimiter) Locked(context.Context, string) bool { return true }
func (lockedLimiter) Succeed(context.Context, string)     {}

// Console login shares the account lockout, including for the configured
// operator, before any credential or database check runs.
func TestLoginHonoursAccountLockout(t *testing.T) {
	h := &Handler{User: "admin", Pass: "right", Attempts: lockedLimiter{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login",
		strings.NewReader(`{"username":"admin","password":"right"}`))
	h.Login(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}
