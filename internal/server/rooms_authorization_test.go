package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zekumo/internal/auth"
	"zekumo/internal/realtime"
)

// Exercise the actual registered route and its authentication/tenant guards.
// A database is intentionally absent: all cases must stop before data access.
func TestRoomMonitorAuthenticationGuards(t *testing.T) {
	issuer := auth.NewTokenIssuer("synthetic-room-monitor-test-secret", time.Hour)
	player, err := issuer.IssuePlayer("player", "game", "Player")
	if err != nil {
		t.Fatal(err)
	}
	account, err := issuer.IssueAccount("account", "Account")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := issuer.IssueAdmin("11111111-1111-4111-8111-111111111111", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	rt := router{ServeMux: http.NewServeMux(), d: &deps{authH: &auth.Handler{Issuer: issuer}, hub: realtime.NewHub(nil, issuer)}}
	registerAdmin(rt)
	for _, tc := range []struct {
		name, token, workspace string
		status                 int
	}{
		{"anonymous", "", "", http.StatusUnauthorized},
		{"player role", player, "", http.StatusForbidden},
		{"account role", account, "", http.StatusForbidden},
		{"admin missing workspace", admin, "", http.StatusBadRequest},
		{"admin malformed workspace", admin, "invalid", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/api/games/22222222-2222-4222-8222-222222222222/rooms", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.workspace != "" {
				req.Header.Set("X-Zekumo-Workspace-ID", tc.workspace)
			}
			res := httptest.NewRecorder()
			rt.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", res.Code, tc.status, res.Body.String())
			}
		})
	}
}
