package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

func phaseWebhooks(s *state) {
	// WebHooks: local receiver verifies the HMAC signature end to end.
	type received struct {
		event string
		valid bool
	}
	recvCh := make(chan received, 16)
	var hookSecret atomic.Value
	hookSecret.Store("")
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(hookSecret.Load().(string)))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		recvCh <- received{
			event: r.Header.Get("X-Zekumo-Event"),
			valid: r.Header.Get("X-Zekumo-Signature") == want,
		}
		w.WriteHeader(200)
	}))
	defer receiver.Close()

	var wh struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	step("create webhook", call("POST", "/admin/api/games/"+s.gameID+"/webhooks", s.adminToken,
		map[string]string{"url": receiver.URL, "events": "player.login,webhook.test"}, &wh))
	hookSecret.Store(wh.Secret)

	var testRes struct {
		OK bool `json:"ok"`
	}
	step("webhook test delivery", call("POST", "/admin/api/webhooks/"+wh.ID+"/test", s.adminToken, map[string]any{}, &testRes))
	step("test delivery accepted", boolErr(testRes.OK, "test delivery not ok"))
	select {
	case got := <-recvCh:
		step("test event signature valid", boolErr(got.event == "webhook.test" && got.valid, "got %+v", got))
	case <-time.After(3 * time.Second):
		step("test event signature valid", fmt.Errorf("no delivery received"))
	}

	step("trigger player.login event", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "guest", "device_id": "device-alice-001"}, nil))
	select {
	case got := <-recvCh:
		step("login event delivered + signed", boolErr(got.event == "player.login" && got.valid, "got %+v", got))
	case <-time.After(5 * time.Second):
		step("login event delivered + signed", fmt.Errorf("no delivery received"))
	}
	step("webhook deliveries recorded", call("GET", "/admin/api/webhooks/"+wh.ID+"/deliveries", s.adminToken, nil, nil))
}
