package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func localGet(t *testing.T, l *Local, rawURL string) *httptest.ResponseRecorder {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(u.Path, "/storage")+"?"+u.RawQuery, nil)
	rec := httptest.NewRecorder()
	l.Handler().ServeHTTP(rec, req)
	return rec
}

func TestLocalDownloadNeverRendersInline(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(t.TempDir(), "http://zekumo.test", "secret", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	const key = "releases/g/1/web-any/game.html"
	if err := l.Put(ctx, key, strings.NewReader("<script>alert(1)</script>"), 0); err != nil {
		t.Fatal(err)
	}
	signed, err := l.PresignDownload(ctx, key, "game.html")
	if err != nil {
		t.Fatal(err)
	}

	rec := localGet(t, l, signed)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed download: status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	// Dropping or editing fn on a valid URL must break the signature rather
	// than turn the download into an inline page.
	u, _ := url.Parse(signed)
	for name, edit := range map[string]func(url.Values){
		"fn removed": func(q url.Values) { q.Del("fn") },
		"fn changed": func(q url.Values) { q.Set("fn", "other.html") },
	} {
		q := u.Query()
		edit(q)
		tampered := *u
		tampered.RawQuery = q.Encode()
		if rec := localGet(t, l, tampered.String()); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
	}
}
