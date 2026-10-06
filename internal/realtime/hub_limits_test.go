package realtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func limitClient(h *Hub, game, id string) *Client {
	c := &Client{hub: h, playerID: id, gameID: game, send: make(chan []byte, 256), subs: map[string]struct{}{}}
	h.space(game).clients[id] = c
	return c
}

func limitError(t *testing.T, c *Client, want string) {
	t.Helper()
	select {
	case raw := <-c.send:
		var env struct {
			Type string `json:"type"`
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "error" || env.Data.Code != want {
			t.Fatalf("got %s; want error %s", raw, want)
		}
	default:
		t.Fatal("missing error")
	}
}

func TestRoomCreateValidation(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"unknown":1}`, `{"max_players":-1}`, `{"max_players":201}`, `{"max_players":"20"}`, `{} {}`, `{"name":"` + strings.Repeat("x", 129) + `"}`, `{"meta":"` + strings.Repeat("x", maxRoomMetaBytes) + `"}`} {
		t.Run(raw[:min(len(raw), 40)], func(t *testing.T) {
			h := NewHub(nil, nil)
			c := limitClient(h, "g", "p")
			h.dispatch(c, Envelope{Type: "room.create", Data: json.RawMessage(raw)})
			limitError(t, c, "bad_request")
			if c.room != nil || len(h.games["g"].rooms) != 0 {
				t.Fatal("invalid create changed membership")
			}
		})
	}
	h := NewHub(nil, nil)
	c := limitClient(h, "g", "p")
	h.dispatch(c, Envelope{Type: "room.create", Data: json.RawMessage(`{}`)})
	if c.room == nil || c.room.MaxPlayers != 20 {
		t.Fatal("default capacity not applied")
	}
}

func TestLockedRoomJoin(t *testing.T) {
	h := NewHub(nil, nil)
	owner := limitClient(h, "g", "owner")
	joiner := limitClient(h, "g", "joiner")
	h.dispatch(owner, Envelope{Type: "room.create", Data: json.RawMessage(`{"locked":true}`)})
	data, _ := json.Marshal(map[string]string{"room_id": owner.room.ID})
	h.dispatch(joiner, Envelope{Type: "room.join", Data: data})
	limitError(t, joiner, "room_locked")
	if joiner.room != nil || len(owner.room.members) != 1 {
		t.Fatal("locked room admitted member")
	}
}

func TestRoomQuotaAndPayloadLimit(t *testing.T) {
	h := NewHub(nil, nil)
	c := limitClient(h, "g", "p")
	g := h.games["g"]
	for i := 0; i < maxRoomsPerGame; i++ {
		id := string(rune(i))
		g.rooms[id] = &Room{ID: id}
	}
	h.dispatch(c, Envelope{Type: "room.create", Data: json.RawMessage(`{}`)})
	limitError(t, c, "room_limit")
	g.rooms = map[string]*Room{}
	h.dispatch(c, Envelope{Type: "room.create", Data: json.RawMessage(`{}`)})
	<-c.send
	for _, typ := range []string{"room.state", "room.msg"} {
		h.dispatch(c, Envelope{Type: typ, Data: json.RawMessage(`"` + strings.Repeat("x", maxRoomPayloadBytes) + `"`)})
		limitError(t, c, "bad_payload")
	}
	if len(c.room.states) != 0 {
		t.Fatal("oversized state stored")
	}
}

func TestRoomLobbyPaginationPrivacy(t *testing.T) {
	h := NewHub(nil, nil)
	c := limitClient(h, "g", "p")
	for i := 0; i < 60; i++ {
		id := string(rune('A' + i))
		h.games["g"].rooms[id] = &Room{ID: id, members: map[string]*Client{"secret": c}, states: map[string]json.RawMessage{"secret": json.RawMessage(`"private-state"`)}}
	}
	h.dispatch(c, Envelope{Type: "room.list", Data: json.RawMessage(`{"limit":999}`)})
	raw := <-c.send
	var env struct {
		Data struct {
			Rooms []map[string]any `json:"rooms"`
			Total int              `json:"total"`
			Limit int              `json:"limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Rooms) != 50 || env.Data.Total != 60 || env.Data.Limit != 50 {
		t.Fatalf("incorrect page %s", raw)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "private-state") || strings.Contains(string(raw), `"members"`) {
		t.Fatal("lobby leaks member state")
	}
	h.dispatch(c, Envelope{Type: "room.list", Data: json.RawMessage(`{"offset":999,"limit":2}`)})
	raw = <-c.send
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Rooms) != 0 {
		t.Fatal("past-end page not empty")
	}
}

func TestConnectionReservationsBoundedAndReclaimed(t *testing.T) {
	h := NewHub(nil, nil)
	for i := 0; i < maxHubGames; i++ {
		if !h.reserveConnectionLocked(string(rune(i + 1))) {
			t.Fatal("premature game limit")
		}
	}
	if h.reserveConnectionLocked("extra") {
		t.Fatal("game limit not enforced")
	}
	g := h.games[string(rune(1))]
	g.pending--
	h.pruneSpaceLocked(string(rune(1)))
	if !h.reserveConnectionLocked("extra") {
		t.Fatal("failed to reclaim empty game")
	}
	h = NewHub(nil, nil)
	for i := 0; i < maxHubClients; i++ {
		if !h.reserveConnectionLocked("g") {
			t.Fatal("premature connection limit")
		}
	}
	if h.reserveConnectionLocked("g") {
		t.Fatal("connection limit not enforced")
	}
}

func TestMessageTokenBudget(t *testing.T) {
	c := &Client{}
	now := time.Now()
	for i := 0; i < 120; i++ {
		if !c.allowMessage(now) {
			t.Fatal("premature rate limit")
		}
	}
	if c.allowMessage(now) {
		t.Fatal("burst not bounded")
	}
	if !c.allowMessage(now.Add(time.Second)) {
		t.Fatal("budget did not refill")
	}
}

func TestOutboundByteAccounting(t *testing.T) {
	h := NewHub(nil, nil)
	c := limitClient(h, "g", "p")
	payload := []byte("hello")
	c.enqueue(payload)
	if c.queuedBytes.Load() != 5 || h.queuedBytes.Load() != 5 {
		t.Fatal("enqueue bytes not reserved")
	}
	c.releaseQueued(<-c.send)
	if c.queuedBytes.Load() != 0 || h.queuedBytes.Load() != 0 {
		t.Fatal("dequeue bytes not released")
	}
	c.enqueue(payload)
	c.close(1000, "")
	if c.queuedBytes.Load() != 0 || h.queuedBytes.Load() != 0 {
		t.Fatal("close did not release queued bytes")
	}
	c.enqueue(payload)
	if h.queuedBytes.Load() != 0 {
		t.Fatal("closed client retained bytes")
	}
}

func TestOutboundByteCapClosesSlowClient(t *testing.T) {
	h := NewHub(nil, nil)
	c := limitClient(h, "g", "p")
	c.enqueue(make([]byte, maxClientQueuedBytes))
	c.enqueue([]byte("x"))
	if !c.closed {
		t.Fatal("client byte cap not enforced")
	}
	if h.queuedBytes.Load() != 0 || c.queuedBytes.Load() != 0 {
		t.Fatal("overflow bytes leaked")
	}
	c = limitClient(h, "g", "q")
	h.queuedBytes.Store(maxHubQueuedBytes)
	c.enqueue([]byte("x"))
	if !c.closed || h.queuedBytes.Load() != maxHubQueuedBytes {
		t.Fatal("hub byte cap not enforced or accounting corrupted")
	}
}
