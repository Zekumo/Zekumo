package logs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool connects to the development database, skipping when absent so
// the suite still runs on a bare checkout.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://zekumo:zekumo@localhost:5432/zekumo"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("no Postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("no Postgres at %s: %v", url, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func randomTag() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "test-" + hex.EncodeToString(b)
}

// TestShutdownFlushesBufferedEntries covers the promise the deployment docs
// make: a rolling restart does not lose the last, still-buffered batch.
func TestShutdownFlushesBufferedEntries(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, pool, 14)

	tag := randomTag()
	const n = 25
	for i := range n {
		svc.Write("", "info", tag, "shutdown", "buffered entry", map[string]any{"i": i})
	}

	// Cancel immediately: the flush ticker has not fired and the batch is well
	// under batchSize, so these rows exist only in memory at this point.
	cancel()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer waitCancel()
	svc.Wait(waitCtx)

	countCtx, countCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer countCancel()
	var got int
	if err := pool.QueryRow(countCtx,
		`SELECT count(*) FROM app_logs WHERE source = $1`, tag).Scan(&got); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 5*time.Second)
		defer cc()
		_, _ = pool.Exec(c, `DELETE FROM app_logs WHERE source = $1`, tag)
	})

	if got != n {
		t.Errorf("flushed %d of %d buffered entries on shutdown", got, n)
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	// A multi-byte rune straddling the limit must not be cut in half:
	// Postgres rejects invalid UTF-8 and the whole batch would fail.
	s := strings.Repeat("a", 1999) + "世界"
	got := truncate(s, maxMessageLen)
	if len(got) > maxMessageLen {
		t.Errorf("truncate returned %d bytes, want <= %d", len(got), maxMessageLen)
	}
	if !isValidUTF8(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got[len(got)-4:])
	}
	if truncate("short", maxMessageLen) != "short" {
		t.Error("truncate must leave short strings alone")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func TestEscapeLikeNeutralizesWildcards(t *testing.T) {
	for in, want := range map[string]string{
		"%":      `\%`,
		"_":      `\_`,
		`a\b`:    `a\\b`,
		"50%_up": `50\%\_up`,
		"plain":  "plain",
	} {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
