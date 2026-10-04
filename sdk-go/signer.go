// signer.go is the server-side half of the SDK: writing game-level KV.
//
// Reads go through Client.KV with a player token, but writes are signed with
// the game's app_secret — which must never ship inside a game client, since
// anyone holding it can rewrite every game's config. Use Signer from your own
// game server, a CI job, or an ops tool.
//
//	sg := minicloud.NewSigner("https://api.example.com", appID, appSecret)
//	sg.Put(ctx, "public", "event", map[string]any{"double_drop": true}, 0)
//	sg.Put(ctx, "public", "flash_sale", cfg, 2*time.Hour)  // auto-expires
//	sg.Delete(ctx, "public", "event")
//
// The signature is HMAC-SHA256 over "{timestamp}.{method}.{path}.{body}"
// keyed by app_secret, sent alongside a unix-seconds timestamp the server
// requires to be within ±5 minutes — so a captured request cannot be replayed
// later. A clock more than 5 minutes off is the usual cause of a rejected
// signature.

package minicloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxKVValue mirrors the server's own 64KB cap. Checking here turns a wasted
// round trip into an immediate error with a clearer message.
const maxKVValue = 64 << 10

// maxKVTTL is the server's ceiling on TTL: one year in seconds.
const maxKVTTL = 365 * 24 * 3600

// Signer writes game-level KV with app_secret credentials. It is safe for
// concurrent use.
type Signer struct {
	baseURL   string
	appID     string
	appSecret string
	http      *http.Client
}

// NewSigner builds a Signer. Keep app_secret out of client binaries and out
// of version control — read it from the environment or a secret store.
func NewSigner(baseURL, appID, appSecret string) *Signer {
	return &Signer{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		appID:     appID,
		appSecret: appSecret,
		http:      &http.Client{Timeout: defaultTimeout},
	}
}

// HTTPClient replaces the default client, for a proxy or a shared pool.
func (s *Signer) HTTPClient(c *http.Client) { s.http = c }

// sign computes the header value for one request. The string signed is
// timestamp, method, path and body joined by dots — the body is included so
// nobody can swap the payload of a captured request.
func (s *Signer) sign(ts, method, path string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(s.appSecret))
	mac.Write([]byte(ts + "." + method + "." + path + "."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// do issues one signed request. target may carry a query string, but only the
// path is signed — the server verifies against r.URL.Path (see internal/kv),
// so signing the query too would make every TTL write fail.
func (s *Signer) do(ctx context.Context, method, target string, body []byte, out any) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	path, _, _ := strings.Cut(target, "?") // sign the path, send the query

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+target, reader)
	if err != nil {
		return fmt.Errorf("minicloud: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-MC-App-Id", s.appID)
	req.Header.Set("X-MC-Timestamp", ts)
	req.Header.Set("X-MC-Signature", s.sign(ts, method, path, body))

	res, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("minicloud: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		return decodeError(res)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Put writes a config value. ttl 0 stores it permanently; otherwise the value
// disappears once the TTL elapses, which is how a timed event switch turns
// itself off without anyone remembering to.
func (s *Signer) Put(ctx context.Context, namespace, key string, value any, ttl time.Duration) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("minicloud: encode value: %w", err)
	}
	if len(body) > maxKVValue {
		return fmt.Errorf("minicloud: value is %d bytes, over the 64KB limit", len(body))
	}

	path := "/v1/kv/" + esc(namespace) + "/" + esc(key)
	if ttl > 0 {
		secs := int64(ttl.Seconds())
		if secs < 1 || secs > maxKVTTL {
			return fmt.Errorf("minicloud: ttl must be between 1s and 365 days, got %s", ttl)
		}
		path += "?ttl=" + strconv.FormatInt(secs, 10)
	}
	return s.do(ctx, http.MethodPut, path, body, nil)
}

// Delete removes a config value. A DELETE has no body, so the signature
// covers an empty one.
func (s *Signer) Delete(ctx context.Context, namespace, key string) error {
	return s.do(ctx, http.MethodDelete, "/v1/kv/"+esc(namespace)+"/"+esc(key), nil, nil)
}
