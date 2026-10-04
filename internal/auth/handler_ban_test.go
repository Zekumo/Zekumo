package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"minicloud/internal/repo"
)

type failingBanChecker struct{}

func (failingBanChecker) TokenBanned(context.Context, string) bool { return false }

func (failingBanChecker) ActiveBan(context.Context, string) (*repo.PlayerBan, error) {
	return nil, errors.New("database unavailable")
}

func TestFinishLoginFailsClosedWhenBanLookupFails(t *testing.T) {
	h := &Handler{Bans: failingBanChecker{}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	h.finishLogin(w, r, &repo.Player{ID: "p1", GameID: "g1"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(w.Body.String(), "ban_check_unavailable") {
		t.Fatalf("response did not expose stable ban-check error code: %s", w.Body.String())
	}
}
