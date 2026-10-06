package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"zekumo/internal/auth"
)

func TestWebSocketRoomLifecycleAndReplacement(t *testing.T) {
	issuer := auth.NewTokenIssuer("synthetic-websocket-regression-secret", time.Hour)
	h := NewHub(nil, issuer)
	authH := &auth.Handler{Issuer: issuer}
	server := httptest.NewServer(authH.Middleware(auth.RolePlayer, http.HandlerFunc(h.ServeWS)))
	defer server.Close()
	read := func(c *websocket.Conn, want string) Envelope {
		t.Helper()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		var env Envelope
		if err := c.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		if env.Type != want {
			t.Fatalf("got %q want %q: %s", env.Type, want, env.Data)
		}
		return env
	}
	dial := func(player string) *websocket.Conn {
		t.Helper()
		token, err := issuer.IssuePlayer(player, "game", "Player")
		if err != nil {
			t.Fatal(err)
		}
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?token="+token, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		read(c, "welcome")
		return c
	}
	send := func(c *websocket.Conn, typ string, data any) {
		t.Helper()
		if err := c.WriteJSON(map[string]any{"type": typ, "data": data}); err != nil {
			t.Fatal(err)
		}
	}
	owner := dial("owner")
	send(owner, "room.create", map[string]any{"name": "Fixture"})
	created := read(owner, "room.created")
	var room Room
	if err := json.Unmarshal(created.Data, &room); err != nil {
		t.Fatal(err)
	}
	member := dial("member")
	send(member, "room.join", map[string]any{"room_id": room.ID})
	read(member, "room.joined")
	read(owner, "room.member_joined")
	send(owner, "room.update", map[string]any{"locked": true})
	read(owner, "room.updated")
	read(member, "room.updated")
	send(owner, "room.kick", map[string]any{"player_id": "member"})
	read(owner, "room.member_left")
	read(owner, "room.kick_ok")
	read(member, "room.kicked")
	send(member, "room.join", map[string]any{"room_id": room.ID})
	read(member, "error")
	_ = member.Close()
	replacement := dial("owner")
	send(replacement, "room.get", map[string]any{})
	read(replacement, "error") // Reconnection does not persist the former room.
	send(replacement, "room.create", map[string]any{"name": "Replacement"})
	read(replacement, "room.created")
	send(replacement, "room.get", map[string]any{})
	read(replacement, "room.info")
	_ = owner.Close()
	_ = replacement.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		clean := len(h.games) == 0 && h.queuedBytes.Load() == 0
		h.mu.Unlock()
		if clean {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected sockets did not release game/queue state")
		}
		time.Sleep(time.Millisecond)
	}
}
