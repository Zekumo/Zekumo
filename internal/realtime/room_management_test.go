package realtime

import (
	"encoding/json"
	"strings"
	"testing"
)

func managementFixture() (*Hub, *Client, *Client, *Room) {
	h := NewHub(nil, nil)
	owner := &Client{hub: h, gameID: "game-a", playerID: "owner", nickname: "Owner", send: make(chan []byte, 64)}
	peer := &Client{hub: h, gameID: "game-a", playerID: "peer", nickname: "Peer", send: make(chan []byte, 64)}
	r := &Room{ID: "room-a", OwnerID: owner.playerID, Name: "Before", MaxPlayers: 4, Meta: json.RawMessage(`{"map":"old"}`), members: map[string]*Client{owner.playerID: owner, peer.playerID: peer}, states: map[string]json.RawMessage{peer.playerID: json.RawMessage(`{"x":1}`)}}
	owner.room, peer.room = r, r
	g := h.space(owner.gameID)
	g.rooms[r.ID] = r
	g.clients[owner.playerID], g.clients[peer.playerID] = owner, peer
	return h, owner, peer, r
}

func managementEvent(t *testing.T, c *Client, typ string) Envelope {
	t.Helper()
	select {
	case raw := <-c.send:
		var e Envelope
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		if e.Type != typ {
			t.Fatalf("event = %q, want %q; %s", e.Type, typ, raw)
		}
		return e
	default:
		t.Fatalf("missing event %q", typ)
		return Envelope{}
	}
}
func managementError(t *testing.T, c *Client, code string) {
	t.Helper()
	e := managementEvent(t, c, "error")
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(e.Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != code {
		t.Fatalf("error = %q, want %q", body.Code, code)
	}
}
func managementQuiet(t *testing.T, c *Client) {
	t.Helper()
	select {
	case raw := <-c.send:
		t.Fatalf("unexpected message %s", raw)
	default:
	}
}

func TestRoomManagementUpdateAtomicAndBroadcast(t *testing.T) {
	h, owner, peer, r := managementFixture()
	h.handleRoomUpdate(owner, json.RawMessage(`{"name":"After","max_players":2,"meta":{"map":"new"},"locked":true}`))
	if r.Name != "After" || r.MaxPlayers != 2 || !r.Locked || string(r.Meta) != `{"map":"new"}` {
		t.Fatalf("incorrect room update: %+v", r)
	}
	for _, c := range []*Client{owner, peer} {
		e := managementEvent(t, c, "room.updated")
		var got Room
		if err := json.Unmarshal(e.Data, &got); err != nil {
			t.Fatal(err)
		}
		if !got.Locked || len(got.Members) != 2 || got.OwnerID != owner.playerID {
			t.Fatalf("incorrect snapshot: %+v", got)
		}
	}
	h.handleRoomUpdate(owner, json.RawMessage(`{"name":"Must not apply","max_players":1}`))
	managementError(t, owner, "invalid_max_players")
	if r.Name != "After" || r.MaxPlayers != 2 {
		t.Fatal("invalid patch partially applied")
	}
	managementQuiet(t, peer)
	h.handleRoomUpdate(owner, json.RawMessage(`{"locked":false,"meta":null}`))
	managementEvent(t, owner, "room.updated")
	managementEvent(t, peer, "room.updated")
	if r.Locked || string(r.Meta) != "null" || r.Name != "After" {
		t.Fatal("partial update did not preserve or clear fields correctly")
	}
}

func TestRoomManagementRejectsInvalidPatches(t *testing.T) {
	tests := []struct{ name, data, code string }{
		{"malformed", `{"name":`, "bad_json"},
		{"null", `null`, "bad_json"},
		{"array", `[]`, "bad_json"},
		{"unknown", `{"owner_id":"peer"}`, "bad_json"},
		{"trailing", `{} {}`, "bad_json"},
		{"wrong type", `{"locked":"yes"}`, "bad_json"},
		{"zero capacity", `{"max_players":0}`, "invalid_max_players"},
		{"large capacity", `{"max_players":201}`, "invalid_max_players"},
		{"name byte limit", `{"name":"` + strings.Repeat("界", 43) + `"}`, "invalid_name"},
		{"metadata limit", `{"meta":"` + strings.Repeat("x", 8191) + `"}`, "invalid_meta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, owner, peer, r := managementFixture()
			h.handleRoomUpdate(owner, json.RawMessage(tt.data))
			managementError(t, owner, tt.code)
			if r.Name != "Before" || r.MaxPlayers != 4 || r.Locked {
				t.Fatal("invalid request mutated room")
			}
			managementQuiet(t, peer)
		})
	}
}

func TestRoomManagementOwnershipAndScope(t *testing.T) {
	for _, action := range []string{"update", "kick", "transfer"} {
		t.Run(action, func(t *testing.T) {
			h, owner, peer, r := managementFixture()
			switch action {
			case "update":
				h.handleRoomUpdate(peer, json.RawMessage(`{"name":"stolen"}`))
			case "kick":
				h.handleRoomKick(peer, json.RawMessage(`{"player_id":"owner"}`))
			case "transfer":
				h.handleRoomTransfer(peer, json.RawMessage(`{"player_id":"peer"}`))
			}
			managementError(t, peer, "not_room_owner")
			if r.OwnerID != owner.playerID || len(r.members) != 2 || r.Name != "Before" {
				t.Fatal("non-owner modified room")
			}
			managementQuiet(t, owner)
		})
	}
	h, owner, _, r := managementFixture()
	other := &Client{hub: h, gameID: "game-b", playerID: owner.playerID, room: r, send: make(chan []byte, 8)}
	h.handleRoomGet(other, json.RawMessage(`{}`))
	managementError(t, other, "not_in_room")
	// Even a same-player stale/replaced connection is not a member.
	other.gameID = owner.gameID
	h.handleRoomUpdate(other, json.RawMessage(`{"name":"stolen"}`))
	managementError(t, other, "not_in_room")
	owner.room = nil
	h.handleRoomGet(owner, json.RawMessage(`{}`))
	managementError(t, owner, "not_in_room")
}

func TestRoomManagementKickLifecycle(t *testing.T) {
	h, owner, peer, r := managementFixture()
	h.handleRoomKick(owner, json.RawMessage(`{"player_id":"peer"}`))
	managementEvent(t, owner, "room.member_left")
	ack := managementEvent(t, owner, "room.kick_ok")
	var got map[string]string
	if err := json.Unmarshal(ack.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got["player_id"] != "peer" || got["room_id"] != r.ID {
		t.Fatalf("wrong kick acknowledgement: %v", got)
	}
	event := managementEvent(t, peer, "room.kicked")
	if err := json.Unmarshal(event.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got["reason"] != "removed_by_owner" || got["room_id"] != r.ID {
		t.Fatalf("wrong kick event: %v", got)
	}
	if peer.room != nil || r.members[peer.playerID] != nil || r.states[peer.playerID] != nil || len(r.members) != 1 {
		t.Fatal("kick left membership or state behind")
	}
	if h.games[owner.gameID].clients[peer.playerID] != peer {
		t.Fatal("kick disconnected the player")
	}
	h.handleRoomGet(peer, json.RawMessage(`{}`))
	managementError(t, peer, "not_in_room")
	h.handleRoomKick(owner, json.RawMessage(`{"player_id":"peer"}`))
	managementError(t, owner, "member_not_found")
	h.handleRoomKick(owner, json.RawMessage(`{"player_id":"owner"}`))
	managementError(t, owner, "invalid_target")
	if h.games[owner.gameID].rooms[r.ID] != r {
		t.Fatal("owner's room removed")
	}
}

func TestRoomManagementTransferAndGet(t *testing.T) {
	h, owner, peer, r := managementFixture()
	h.handleRoomTransfer(owner, json.RawMessage(`{"player_id":"peer"}`))
	if r.OwnerID != peer.playerID {
		t.Fatal("owner not transferred")
	}
	managementEvent(t, owner, "room.owner_changed")
	managementEvent(t, owner, "room.transferred")
	managementEvent(t, peer, "room.owner_changed")
	managementQuiet(t, peer)
	h.handleRoomUpdate(owner, json.RawMessage(`{"locked":true}`))
	managementError(t, owner, "not_room_owner")
	h.handleRoomGet(owner, json.RawMessage(`{}`))
	e := managementEvent(t, owner, "room.info")
	var got Room
	if err := json.Unmarshal(e.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != peer.playerID || len(got.Members) != 2 {
		t.Fatalf("bad room.info: %+v", got)
	}
	// Self-transfer is an idempotent success, with the same acknowledgement.
	h.handleRoomTransfer(peer, json.RawMessage(`{"player_id":"peer"}`))
	managementEvent(t, peer, "room.owner_changed")
	managementEvent(t, peer, "room.transferred")
	managementEvent(t, owner, "room.owner_changed")
	h.leaveRoomLocked(peer)
	if r.OwnerID != owner.playerID {
		t.Fatal("owner departure did not promote remaining member")
	}
}

func TestRoomManagementInvalidTargets(t *testing.T) {
	for _, action := range []string{"kick", "transfer"} {
		for _, tt := range []struct{ data, code string }{
			{`{"player_id":`, "bad_json"}, {`null`, "bad_json"}, {`{"player_id":12}`, "bad_json"}, {`{"player_id":"peer","game_id":"other"}`, "bad_json"}, {`{}`, "invalid_target"}, {`{"player_id":"outsider"}`, "member_not_found"},
		} {
			t.Run(action+tt.data, func(t *testing.T) {
				h, owner, peer, r := managementFixture()
				if action == "kick" {
					h.handleRoomKick(owner, json.RawMessage(tt.data))
				} else {
					h.handleRoomTransfer(owner, json.RawMessage(tt.data))
				}
				managementError(t, owner, tt.code)
				managementQuiet(t, peer)
				if len(r.members) != 2 || r.OwnerID != owner.playerID {
					t.Fatal("invalid target mutated room")
				}
			})
		}
	}
}

func TestRoomManagementExactLimitsAndEmptyPatch(t *testing.T) {
	h, owner, peer, r := managementFixture()
	patch := `{"name":"` + strings.Repeat("n", 128) + `","max_players":200,"meta":"` + strings.Repeat("m", 8190) + `"}`
	h.handleRoomUpdate(owner, json.RawMessage(patch))
	managementEvent(t, owner, "room.updated")
	managementEvent(t, peer, "room.updated")
	if len(r.Name) != 128 || r.MaxPlayers != 200 || len(r.Meta) != 8192 {
		t.Fatal("exact limits rejected")
	}
	h.handleRoomUpdate(owner, json.RawMessage(`{}`))
	managementEvent(t, owner, "room.updated")
	managementEvent(t, peer, "room.updated")
	if len(r.Name) != 128 || r.MaxPlayers != 200 || len(r.Meta) != 8192 {
		t.Fatal("empty update changed settings")
	}
}

func TestRoomManagementGetRejectsMalformedData(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{"room_id":"other"}`, `{} {}`, `{`} {
		h, owner, peer, _ := managementFixture()
		h.handleRoomGet(owner, json.RawMessage(data))
		managementError(t, owner, "bad_json")
		managementQuiet(t, peer)
	}
}
