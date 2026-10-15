package sso

import (
	"context"
	"errors"
	"testing"

	"zekumo/internal/auth"
)

// lockedLimiter reports every identity as locked and remembers what it was
// asked about.
type lockedLimiter struct{ asked []string }

func (l *lockedLimiter) Fail(context.Context, string) bool { return true }
func (l *lockedLimiter) Locked(_ context.Context, identity string) bool {
	l.asked = append(l.asked, identity)
	return true
}
func (l *lockedLimiter) Succeed(context.Context, string) {}

// Binding a player and in-game SSO login both verify account passwords, so
// they must honour the same lockout as /sso/api/login. The zero-value repos
// would panic if the lockout were checked after the database lookup.
func TestResolveAccountHonoursAccountLockout(t *testing.T) {
	limiter := &lockedLimiter{}
	p := Provider{Attempts: limiter}
	_, err := p.resolveAccount(context.Background(), "game", auth.Credentials{Username: " Alice ", Password: "x"})
	if !errors.Is(err, auth.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if len(limiter.asked) != 1 || limiter.asked[0] != auth.AccountIdentity("alice") {
		t.Fatalf("lockout checked %v, want the shared account identity", limiter.asked)
	}
}
