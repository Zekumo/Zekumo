package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var (
	base            = env("ZEKUMO_URL", "http://localhost:8080")
	wsBase          = env("ZEKUMO_WS", "ws://localhost:8080")
	failed          = 0
	activeWorkspace = ""
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func step(name string, err error) {
	if err != nil {
		failed++
		fmt.Printf("FAIL  %-42s %v\n", name, err)
	} else {
		fmt.Printf("ok    %s\n", name)
	}
}

func call(method, path, token string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, base+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if activeWorkspace != "" && strings.HasPrefix(path, "/admin/api/") {
		req.Header.Set("X-Zekumo-Workspace-ID", activeWorkspace)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e map[string]any
		_ = json.NewDecoder(res.Body).Decode(&e)
		return fmt.Errorf("%s %s -> %d: %v", method, path, res.StatusCode, e)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

type wsClient struct {
	conn *websocket.Conn
	name string
}

func dialWS(token, name string) (*wsClient, error) {
	conn, _, err := websocket.DefaultDialer.Dial(wsBase+"/v1/ws?token="+token, nil)
	if err != nil {
		return nil, err
	}
	c := &wsClient{conn: conn, name: name}
	if _, err := c.expect("welcome"); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *wsClient) send(typ string, data any) error {
	raw, _ := json.Marshal(data)
	return c.conn.WriteJSON(map[string]any{"type": typ, "data": json.RawMessage(raw)})
}

// expect reads frames until one matches typ (skipping unrelated broadcasts).
func (c *wsClient) expect(typ string) (map[string]any, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := c.conn.ReadJSON(&env); err != nil {
			return nil, fmt.Errorf("%s waiting for %q: %w", c.name, typ, err)
		}
		if env.Type == "error" {
			return nil, fmt.Errorf("%s got error frame while waiting for %q: %s", c.name, typ, env.Data)
		}
		if env.Type == typ {
			var data map[string]any
			_ = json.Unmarshal(env.Data, &data)
			return data, nil
		}
	}
	return nil, fmt.Errorf("%s timed out waiting for %q", c.name, typ)
}

func expectErr(err error) error {
	if err == nil {
		return fmt.Errorf("expected an error but the call succeeded")
	}
	return nil
}

func boolErr(ok bool, format string, args ...any) error {
	if !ok {
		return fmt.Errorf(format, args...)
	}
	return nil
}

// rebase points local-driver storage URLs (path /storage/...) at the server
// under test, since BASE_URL inside the server may differ from ZEKUMO_URL.
// S3 presigned URLs pass through untouched.
