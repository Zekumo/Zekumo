// client_test.go verifies what the SDK actually puts on the wire, against a
// real httptest server rather than a mock: the path, the query, the headers
// and the body a caller's method call produces.
//
// Run: go test ./...
//
// The one thing these cannot prove is that the server agrees — for that, the
// signature test below recomputes the HMAC exactly as internal/kv does, so a
// drift in either implementation shows up here.

package zekumo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capture records what the SDK sent, so a test can assert on it afterwards.
type capture struct {
	method  string
	path    string
	query   string
	auth    string
	body    string
	hdr     http.Header
	rawPath string // the path as it arrived, before decoding
}

// serve spins up a server that records one request and replies with the given
// JSON, and returns a Client pointed at it.
func serve(t *testing.T, status int, reply string) (*Client, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.rawPath = r.URL.EscapedPath() // Path is already decoded; this is the wire form
		got.auth, got.body, got.hdr = r.Header.Get("Authorization"), string(raw), r.Header
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return New(Options{AppID: "zk_test", BaseURL: srv.URL}), got
}

func TestLoginStoresTokenAndScopesToApp(t *testing.T) {
	c, got := serve(t, 200, `{"token":"jwt-abc","player":{"id":"p1","nickname":"Ann"}}`)

	login, err := c.Auth.LoginAsGuest(context.Background(), "device-1", "Ann")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Player.ID != "p1" {
		t.Errorf("player id = %q, want p1", login.Player.ID)
	}
	// The token must land on the Client, or every later call is anonymous.
	if c.Token() != "jwt-abc" {
		t.Errorf("token = %q, want jwt-abc", c.Token())
	}
	// A login that forgets app_id authenticates against no game at all.
	var sent map[string]any
	_ = json.Unmarshal([]byte(got.body), &sent)
	if sent["app_id"] != "zk_test" {
		t.Errorf("app_id = %v, want zk_test", sent["app_id"])
	}
	if sent["provider"] != "guest" || sent["device_id"] != "device-1" {
		t.Errorf("credentials not sent: %s", got.body)
	}
}

func TestAuthedCallSendsBearer(t *testing.T) {
	c, got := serve(t, 200, `{"entries":[]}`)
	c.SetToken("jwt-xyz")

	if _, err := c.Data.List(context.Background()); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got.auth != "Bearer jwt-xyz" {
		t.Errorf("Authorization = %q, want Bearer jwt-xyz", got.auth)
	}
}

// Update checks run before login, so sending a stale token there would be
// wrong; the public endpoints must stay anonymous even when a token exists.
func TestPublicCallOmitsBearer(t *testing.T) {
	c, got := serve(t, 200, `{"update_available":false}`)
	c.SetToken("jwt-xyz")

	if _, err := c.Updates.Check(context.Background(), CheckOptions{Version: "1.0.0", Platform: "windows"}); err != nil {
		t.Fatalf("check: %v", err)
	}
	if got.auth != "" {
		t.Errorf("Authorization = %q, want none on a public endpoint", got.auth)
	}
	if got.path != "/v1/apps/zk_test/updates/check" {
		t.Errorf("path = %q", got.path)
	}
	if !strings.Contains(got.query, "version=1.0.0") || !strings.Contains(got.query, "platform=windows") {
		t.Errorf("query = %q, want version and platform", got.query)
	}
}

// An unset optional parameter must be absent, not blank: the server treats an
// empty platform as "every platform", which is a different query.
func TestEmptyOptionsAreOmittedFromQuery(t *testing.T) {
	c, got := serve(t, 200, `{"entries":[]}`)

	if _, err := c.Leaderboards.Top(context.Background(), "weekly", TopOptions{Limit: 10}); err != nil {
		t.Fatalf("top: %v", err)
	}
	if strings.Contains(got.query, "scope=") || strings.Contains(got.query, "offset=") {
		t.Errorf("query = %q, want neither scope nor offset", got.query)
	}
	if !strings.Contains(got.query, "limit=10") {
		t.Errorf("query = %q, want limit=10", got.query)
	}
}

// Submit returns the score after the write, which differs from the input in
// incr mode — returning the request value would quietly lie to the caller.
func TestSubmitReturnsResultingScore(t *testing.T) {
	c, _ := serve(t, 200, `{"score":250}`)

	score, err := c.Leaderboards.Submit(context.Background(), "weekly", 100, "incr")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if score != 250 {
		t.Errorf("score = %d, want 250 (the post-increment total)", score)
	}
}

// The server answers one "requests" list for both directions.
func TestFriendRequestsReadsSingleList(t *testing.T) {
	c, _ := serve(t, 200, `{"requests":[{"id":"r1","from_id":"a","to_id":"b","status":"pending"}]}`)

	reqs, err := c.Friends.Requests(context.Background())
	if err != nil {
		t.Fatalf("requests: %v", err)
	}
	if len(reqs) != 1 || reqs[0].ID != "r1" {
		t.Fatalf("requests = %+v, want one request r1", reqs)
	}
}

// A key with a slash must not retarget the request at another endpoint.
func TestPathSegmentsAreEscaped(t *testing.T) {
	c, got := serve(t, 200, `{"key":"a/b","value":1}`)

	var v int
	if err := c.Data.Get(context.Background(), "a/b", &v); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.rawPath != "/v1/player/data/a%2Fb" {
		t.Errorf("wire path = %q, want the slash escaped", got.rawPath)
	}
	// Decoded, the server still sees the original key — escaping must be
	// transparent, not lossy.
	if got.path != "/v1/player/data/a/b" {
		t.Errorf("decoded path = %q, want the original key back", got.path)
	}
}

func TestErrorCarriesServerCode(t *testing.T) {
	c, _ := serve(t, 402, `{"error":{"code":"insufficient_funds","message":"not enough coins"}}`)

	_, err := c.Currency.Spend(context.Background(), "cid", 100, "key-1", "")
	if err == nil {
		t.Fatal("expected an error for HTTP 402")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *zekumo.Error", err)
	}
	if apiErr.Code != "insufficient_funds" || apiErr.Status != 402 {
		t.Errorf("got %+v, want code insufficient_funds status 402", apiErr)
	}
	// IsCode is what call sites use to branch; it must agree.
	if !IsCode(err, "insufficient_funds") {
		t.Error("IsCode did not match the server code")
	}
}

// A gateway in front of the server returns HTML, not the API error shape. The
// SDK still has to produce a usable error rather than a decode failure.
func TestNonJSONErrorStillYieldsCode(t *testing.T) {
	c, _ := serve(t, 502, `<html>Bad Gateway</html>`)

	err := c.Logs.Report(context.Background(), Log{Message: "hi"})
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *zekumo.Error", err)
	}
	if apiErr.Code != "http_502" {
		t.Errorf("code = %q, want http_502", apiErr.Code)
	}
}

// A duplicate spend is a success the caller must be able to detect, not an
// error — retrying after a dropped connection depends on it.
func TestDuplicateSpendIsNotAnError(t *testing.T) {
	c, _ := serve(t, 200, `{"currency_id":"c1","balance":50,"duplicate":true}`)

	res, err := c.Currency.Spend(context.Background(), "c1", 10, "same-key", "")
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	if !res.Duplicate || res.Balance != 50 {
		t.Errorf("got %+v, want duplicate=true balance=50", res)
	}
}

// The signature must match internal/kv byte for byte, including the rule that
// only the path — never the query — is signed.
func TestSignatureMatchesServerRecipe(t *testing.T) {
	var got capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.path, got.query, got.body, got.hdr = r.URL.Path, r.URL.RawQuery, string(raw), r.Header
		w.WriteHeader(200)
	}))
	defer srv.Close()

	const secret = "s3cr3t"
	sg := NewSigner(srv.URL, "zk_test", secret)
	if err := sg.Put(context.Background(), "public", "event", map[string]bool{"on": true}, 2*time.Hour); err != nil {
		t.Fatalf("put: %v", err)
	}

	if got.query != "ttl=7200" {
		t.Errorf("query = %q, want ttl=7200", got.query)
	}
	// Recompute exactly as internal/kv/kv.go does.
	ts := got.hdr.Get("X-Zekumo-Timestamp")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + http.MethodPut + "." + got.path + "."))
	mac.Write([]byte(got.body))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if sig := got.hdr.Get("X-Zekumo-Signature"); sig != want {
		t.Errorf("signature mismatch\n got %s\nwant %s", sig, want)
	}
	if got.hdr.Get("X-Zekumo-App-Id") != "zk_test" {
		t.Errorf("app id header = %q", got.hdr.Get("X-Zekumo-App-Id"))
	}
}

// A DELETE carries no body, and the signature covers an empty one.
func TestSignedDeleteHasEmptyBodyInSignature(t *testing.T) {
	var got capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.path, got.body, got.hdr = r.URL.Path, string(raw), r.Header
		w.WriteHeader(200)
	}))
	defer srv.Close()

	const secret = "s3cr3t"
	if err := NewSigner(srv.URL, "zk_test", secret).
		Delete(context.Background(), "public", "event"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	ts := got.hdr.Get("X-Zekumo-Timestamp")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + http.MethodDelete + "." + got.path + "."))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig := got.hdr.Get("X-Zekumo-Signature"); sig != want {
		t.Errorf("signature mismatch\n got %s\nwant %s", sig, want)
	}
}

// An oversized value must fail locally: the server would reject it anyway,
// and a wasted 64KB upload is a worse way to find out.
func TestOversizedKVValueFailsBeforeSending(t *testing.T) {
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
	}))
	defer srv.Close()

	big := strings.Repeat("x", maxKVValue)
	err := NewSigner(srv.URL, "zk_test", "s").Put(context.Background(), "public", "k", big, 0)
	if err == nil {
		t.Fatal("expected an error for an oversized value")
	}
	if sent {
		t.Error("the request was sent; it should have failed locally")
	}
}

func TestTTLOutOfRangeIsRejected(t *testing.T) {
	sg := NewSigner("http://example.invalid", "zk_test", "s")
	if err := sg.Put(context.Background(), "public", "k", 1, 400*24*time.Hour); err == nil {
		t.Error("expected an error for a TTL over one year")
	}
}

// A cancelled context must abort in flight, which is how a game quits without
// waiting out a network timeout.
func TestContextCancellationAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // outlive the context below
	}))
	defer srv.Close()

	c := New(Options{AppID: "zk_test", BaseURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := c.Player.Me(ctx); err == nil {
		t.Fatal("expected the cancelled context to fail the call")
	}
}

// The gateway URL is derived from the HTTP base URL, so https must become
// wss — a game configured for TLS must not silently fall back to plaintext.
func TestWebSocketURLDerivation(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.example.com", "wss://api.example.com/v1/ws?token=t"},
		{"http://localhost:8080", "ws://localhost:8080/v1/ws?token=t"},
		{"https://example.com/prefix/", "wss://example.com/prefix/v1/ws?token=t"},
	} {
		c := New(Options{AppID: "zk_test", BaseURL: tc.base, Token: "t"})
		got, err := c.Realtime().wsURL()
		if err != nil {
			t.Fatalf("%s: %v", tc.base, err)
		}
		if got != tc.want {
			t.Errorf("%s ->\n got %s\nwant %s", tc.base, got, tc.want)
		}
	}
}

// A token with URL-special characters must survive the query encoding.
func TestWebSocketURLEscapesToken(t *testing.T) {
	c := New(Options{AppID: "zk_test", BaseURL: "https://x.test", Token: "a+b/c=="})
	got, err := c.Realtime().wsURL()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "a+b/c==") {
		t.Errorf("token was not escaped: %s", got)
	}
}

// Realtime must reject a base URL it cannot turn into a WebSocket URL rather
// than dialling something surprising.
func TestWebSocketURLRejectsNonHTTPScheme(t *testing.T) {
	c := New(Options{AppID: "zk_test", BaseURL: "ftp://x.test"})
	if _, err := c.Realtime().wsURL(); err == nil {
		t.Error("expected an error for a non-HTTP base URL")
	}
}

// The token is read and written from several goroutines in a real game; this
// fails under -race if the mutex is ever dropped.
func TestTokenIsRaceFree(t *testing.T) {
	c := New(Options{AppID: "zk_test", BaseURL: "http://x.test"})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			c.SetToken("t")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = c.Token()
	}
	<-done
}

// Handlers are registered and fired from different goroutines; emit copies the
// slice under the lock so a handler registering another handler cannot
// deadlock or race.
func TestRealtimeHandlersAreRaceFree(t *testing.T) {
	rt := New(Options{AppID: "zk_test", BaseURL: "http://x.test"}).Realtime()
	fired := make(chan struct{}, 100)
	rt.On("tick", func(json.RawMessage) { fired <- struct{}{} })

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			rt.On("other", func(json.RawMessage) {})
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		rt.emit("tick", nil)
	}
	<-done
	if len(fired) != 100 {
		t.Errorf("handler fired %d times, want 100", len(fired))
	}
}

// Close must be safe to call twice: a game commonly closes on error and again
// on quit.
func TestRealtimeCloseIsIdempotent(t *testing.T) {
	rt := New(Options{AppID: "zk_test", BaseURL: "http://x.test"}).Realtime()
	if err := rt.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	// Sending after Close must report it rather than panic on a nil socket.
	if err := rt.Ping(); err == nil {
		t.Error("expected an error when sending on a closed client")
	}
}

// Sending before Connect must say so instead of panicking.
func TestRealtimeSendBeforeConnect(t *testing.T) {
	rt := New(Options{AppID: "zk_test", BaseURL: "http://x.test"}).Realtime()
	if err := rt.SyncState(map[string]int{"x": 1}); err == nil {
		t.Error("expected an error when sending before Connect")
	}
}

// Inbox returns the unread count alongside the mail, for the badge.
func TestInboxReturnsUnreadCount(t *testing.T) {
	c, got := serve(t, 200, `{"mail":[{"id":"m1","title":"hi"}],"unread":3}`)

	mail, unread, err := c.Mailbox.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(mail) != 1 || unread != 3 {
		t.Errorf("got %d mail unread=%d, want 1 and 3", len(mail), unread)
	}
	if got.query != "unread_only=true" {
		t.Errorf("query = %q, want unread_only=true", got.query)
	}
}
