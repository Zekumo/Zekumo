package realtime

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// These fixtures exercise only the in-memory protocol with synthetic players.
func regressionClient(h *Hub, game, player string) *Client {
	c := &Client{hub: h, gameID: game, playerID: player, nickname: player, send: make(chan []byte, 256), subs: map[string]struct{}{}}
	h.mu.Lock()
	h.space(game).clients[player] = c
	h.mu.Unlock()
	return c
}

func regressionDispatch(h *Hub, c *Client, typ string, data any) {
	raw, _ := json.Marshal(data)
	h.dispatch(c, Envelope{Type: typ, Data: raw})
}

func regressionMessage(t *testing.T, c *Client, typ string) Envelope {
	t.Helper()
	select {
	case raw := <-c.send:
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != typ {
			t.Fatalf("message type = %q, want %q; data=%s", env.Type, typ, env.Data)
		}
		return env
	default:
		t.Fatalf("missing %s message", typ)
		return Envelope{}
	}
}

func TestRegressionGameIsolationAndRestart(t *testing.T) {
	h := NewHub(nil, nil)
	a := regressionClient(h, "game-a", "same-player")
	b := regressionClient(h, "game-b", "same-player")
	regressionDispatch(h, a, "room.create", map[string]any{"name": "A room"})
	regressionMessage(t, a, "room.created")
	roomID := a.room.ID
	regressionDispatch(h, b, "room.list", map[string]any{})
	list := regressionMessage(t, b, "room.list")
	var result struct {
		Rooms []Room `json:"rooms"`
	}
	if err := json.Unmarshal(list.Data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rooms) != 0 {
		t.Fatalf("game B has %d rooms, want none", len(result.Rooms))
	}
	regressionDispatch(h, b, "room.join", map[string]any{"room_id": roomID})
	regressionMessage(t, b, "error")
	if b.room != nil || len(a.room.members) != 1 {
		t.Fatal("joining absent room changed membership")
	}
	fresh := NewHub(nil, nil)
	if fresh.OnlineCount("game-a") != 0 || len(fresh.games) != 0 {
		t.Fatal("new process must start with empty volatile room state")
	}
}

func TestRegressionDuplicateLeaveAndDisconnectCleanup(t *testing.T) {
	h := NewHub(nil, nil)
	owner := regressionClient(h, "game", "owner")
	member := regressionClient(h, "game", "member")
	regressionDispatch(h, owner, "room.create", map[string]any{"name": "room"})
	regressionMessage(t, owner, "room.created")
	room := owner.room
	regressionDispatch(h, member, "room.join", map[string]any{"room_id": room.ID})
	regressionMessage(t, member, "room.joined")
	regressionMessage(t, owner, "room.member_joined")
	regressionDispatch(h, owner, "room.leave", nil)
	regressionMessage(t, owner, "room.left")
	regressionMessage(t, member, "room.member_left")
	regressionDispatch(h, owner, "room.leave", nil)
	regressionMessage(t, owner, "error")
	if room.OwnerID != member.playerID || len(room.members) != 1 {
		t.Fatal("duplicate leave changed remaining owner or membership")
	}
	h.mu.Lock()
	h.removeClientLocked(owner)
	h.removeClientLocked(owner)
	h.removeClientLocked(member)
	h.removeClientLocked(member)
	remaining := len(h.games)
	h.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d empty game spaces remain after repeated disconnect", remaining)
	}
}

func TestRegressionStaleConnectionCannotChangeReplacement(t *testing.T) {
	h := NewHub(nil, nil)
	old := regressionClient(h, "game", "player")
	regressionDispatch(h, old, "room.create", map[string]any{"name": "old"})
	regressionMessage(t, old, "room.created")
	h.mu.Lock()
	h.removeClientLocked(old)
	h.mu.Unlock()
	replacement := regressionClient(h, "game", "player")
	regressionDispatch(h, replacement, "room.create", map[string]any{"name": "replacement"})
	regressionMessage(t, replacement, "room.created")
	regressionDispatch(h, old, "room.leave", nil)
	h.mu.Lock()
	h.removeClientLocked(old)
	valid := h.games["game"].clients["player"] == replacement && replacement.room != nil && len(replacement.room.members) == 1
	h.mu.Unlock()
	if !valid {
		t.Fatal("stale connection cleanup altered replacement")
	}
}

func TestRegressionConcurrentIndependentRoomLifecycle(t *testing.T) {
	h := NewHub(nil, nil)
	const n = 24
	clients := make([]*Client, n)
	for i := range clients {
		clients[i] = regressionClient(h, fmt.Sprintf("game-%d", i), "player")
	}
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			regressionDispatch(h, c, "room.create", map[string]any{"name": "room"})
			regressionDispatch(h, c, "room.state", map[string]any{"x": 1})
			_ = h.OnlineCount(c.gameID)
			regressionDispatch(h, c, "room.leave", nil)
			h.mu.Lock()
			h.removeClientLocked(c)
			h.mu.Unlock()
		}(c)
	}
	wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.games) != 0 {
		t.Fatalf("%d game spaces leaked after concurrent lifecycle", len(h.games))
	}
}
