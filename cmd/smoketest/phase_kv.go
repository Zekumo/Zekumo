package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// phaseKV exercises the game-level config store. It is the one API with two
// different authentications: players read public namespaces with their token,
// while writes are signed with app_secret so they can only come from a game
// server. Both halves are checked here, plus the rule that a player cannot
// read a private namespace and cannot write at all.
func phaseKV(s *state) {
	fmt.Println("\n-- game kv --")

	// --- signed writes, as a game server would issue them ---

	step("signed put in public namespace",
		signedKV(s, http.MethodPut, "/v1/kv/public/event", []byte(`{"double_drop":true,"ends":"周日"}`)))

	// A private namespace is writable server-side; only reads are restricted.
	step("signed put in private namespace",
		signedKV(s, http.MethodPut, "/v1/kv/secrets/pay_key", []byte(`{"key":"internal-only"}`)))

	// TTL is a query parameter, and only the path is signed — signing the
	// query too would make every TTL write fail verification.
	step("signed put with ttl",
		signedKV(s, http.MethodPut, "/v1/kv/public/flash_sale?ttl=7200", []byte(`{"discount":30}`)))

	// A body that is not JSON is refused: the store hands values straight back
	// to clients, so it must not accept anything a client cannot parse.
	step("non-JSON body rejected",
		expectErr(signedKV(s, http.MethodPut, "/v1/kv/public/broken", []byte(`not json`))))

	// TTL bounds: zero and over a year are both out of range.
	step("ttl=0 rejected",
		expectErr(signedKV(s, http.MethodPut, "/v1/kv/public/bad_ttl?ttl=0", []byte(`1`))))
	step("ttl over a year rejected",
		expectErr(signedKV(s, http.MethodPut, "/v1/kv/public/bad_ttl?ttl=99999999", []byte(`1`))))

	// Namespace and key are constrained to [a-zA-Z0-9_.-]{1,64}.
	step("illegal key rejected",
		expectErr(signedKV(s, http.MethodPut, "/v1/kv/public/bad%20key", []byte(`1`))))

	// --- signature verification ---

	// A tampered signature must fail, or the app_secret proves nothing.
	step("wrong signature rejected", expectErr(signedRaw(s,
		http.MethodPut, "/v1/kv/public/forged", []byte(`{"x":1}`),
		strconv.FormatInt(time.Now().Unix(), 10), "sha256=deadbeef")))

	// A timestamp outside the ±5 minute window must fail, so a captured
	// request cannot be replayed tomorrow.
	oldTS := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	body := []byte(`{"x":1}`)
	step("stale timestamp rejected", expectErr(signedRaw(s,
		http.MethodPut, "/v1/kv/public/replay", body,
		oldTS, sign(s.appSecret, oldTS, http.MethodPut, "/v1/kv/public/replay", body))))

	// Signing the query string (a plausible mistake) must be rejected, which
	// pins down that the server signs the path alone.
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	step("signature over path+query rejected", expectErr(signedRaw(s,
		http.MethodPut, "/v1/kv/public/queried?ttl=60", body,
		ts, sign(s.appSecret, ts, http.MethodPut, "/v1/kv/public/queried?ttl=60", body))))

	// Missing headers entirely: no signature, no write.
	step("unsigned write rejected", expectErr(func() error {
		req, _ := http.NewRequest(http.MethodPut, base+"/v1/kv/public/anon", bytes.NewReader(body))
		return doKV(req)
	}()))

	// --- player reads ---

	var cfg struct {
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Value     struct {
			DoubleDrop bool   `json:"double_drop"`
			Ends       string `json:"ends"`
		} `json:"value"`
	}
	step("alice reads public config", call("GET", "/v1/kv/public/event", s.alice.Token, nil, &cfg))
	step("config value round-tripped", boolErr(cfg.Value.DoubleDrop && cfg.Value.Ends == "周日",
		"value = %+v, want double_drop true ends 周日", cfg.Value))

	// Listing a namespace returns keys without values, so a client cannot
	// enumerate config bodies it was not told to read.
	var keys struct {
		Namespace string `json:"namespace"`
		Keys      []struct {
			Key string `json:"key"`
		} `json:"keys"`
	}
	step("alice lists public namespace", call("GET", "/v1/kv/public", s.alice.Token, nil, &keys))
	step("listing has event and flash_sale", boolErr(len(keys.Keys) == 2, "keys = %+v, want 2", keys.Keys))

	// A private namespace is invisible to players even though the write above
	// succeeded — this is the whole reason the namespace prefix exists.
	step("private namespace read forbidden",
		expectErr(call("GET", "/v1/kv/secrets/pay_key", s.alice.Token, nil, nil)))
	step("private namespace list forbidden",
		expectErr(call("GET", "/v1/kv/secrets", s.alice.Token, nil, nil)))

	// public_ prefixed namespaces are readable too.
	step("signed put in public_ prefixed namespace",
		signedKV(s, http.MethodPut, "/v1/kv/public_ui/theme", []byte(`{"accent":"#6750A4"}`)))
	step("alice reads public_ prefixed namespace",
		call("GET", "/v1/kv/public_ui/theme", s.alice.Token, nil, nil))

	// A missing key is a 404, distinguishable from an empty value.
	step("missing key is 404", expectErr(call("GET", "/v1/kv/public/nothing_here", s.alice.Token, nil, nil)))

	// A player token carries no write authority, whatever the namespace.
	step("player token cannot write", expectErr(call("PUT", "/v1/kv/public/event", s.alice.Token,
		map[string]any{"double_drop": false}, nil)))

	// --- signed delete ---

	step("signed delete", signedKV(s, http.MethodDelete, "/v1/kv/public/event", nil))
	step("deleted key is gone", expectErr(call("GET", "/v1/kv/public/event", s.alice.Token, nil, nil)))
}

// sign reproduces the server's recipe: HMAC-SHA256 over
// "{timestamp}.{method}.{path}.{body}" keyed by app_secret. Only the path goes
// in — never the query — which is why TTL writes sign the bare path.
func sign(secret, ts, method, path string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + method + "." + path + "."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// signedKV issues a correctly signed request, splitting the query off the
// target before signing.
func signedKV(s *state, method, target string, body []byte) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	path, _, _ := strings.Cut(target, "?") // sign the path, send the query
	return signedRaw(s, method, target, body, ts, sign(s.appSecret, ts, method, path, body))
}

// signedRaw sends whatever timestamp and signature it is given, so a test can
// deliberately present a bad one.
func signedRaw(s *state, method, target string, body []byte, ts, sig string) error {
	req, _ := http.NewRequest(method, base+target, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Zekumo-App-Id", s.appID)
	req.Header.Set("X-Zekumo-Timestamp", ts)
	req.Header.Set("X-Zekumo-Signature", sig)
	return doKV(req)
}

// doKV runs the request and turns any non-2xx into an error, matching how
// call() reports failures.
func doKV(req *http.Request) error {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		buf := make([]byte, 512)
		n, _ := res.Body.Read(buf)
		return fmt.Errorf("%s %s -> %d: %s", req.Method, req.URL.Path, res.StatusCode, buf[:n])
	}
	return nil
}
