package storage

import (
	"context"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func signedLifetime(t *testing.T, raw string) time.Duration {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := strconv.ParseInt(u.Query().Get("exp"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return time.Until(time.Unix(exp, 0))
}

func TestLocalPresignDownloadTTL(t *testing.T) {
	local, err := NewLocal(t.TempDir(), "https://api.example.test", "test-secret", 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	standard, err := local.PresignDownload(context.Background(), "releases/a", "a.zip")
	if err != nil {
		t.Fatal(err)
	}
	if got := signedLifetime(t, standard); got < PresignTTL-2*time.Second || got > PresignTTL {
		t.Fatalf("standard lifetime = %s, want about %s", got, PresignTTL)
	}

	export, err := local.PresignDownloadTTL(context.Background(), "exports/a", "a.json", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := signedLifetime(t, export); got < MaxPresignTTL-2*time.Second || got > MaxPresignTTL {
		t.Fatalf("bounded lifetime = %s, want about %s", got, MaxPresignTTL)
	}
}
