package realtime

import (
	"encoding/json"
	"testing"
)

// Keep the previous encoding as a compatibility oracle, including its fallback
// when a value cannot be encoded. These tests do not replace protocol tests.
func previousMessageEncoding(typ string, data any) []byte {
	raw, _ := json.Marshal(data)
	out, _ := json.Marshal(Envelope{Type: typ, Data: raw})
	return out
}

func TestMessageEncodingCompatibility(t *testing.T) {
	values := []any{
		nil, struct{}{}, map[string]string{"value": "<>&\u2028\u2029\"\\\n"},
		json.RawMessage(` { "nested": [true, null, 1.25, "<>&"] } `),
		json.RawMessage(nil), json.RawMessage(`invalid`), make(chan int),
		[]string{}, []string(nil),
	}
	for i, data := range values {
		typ := "type\"<>&\n"
		want := previousMessageEncoding(typ, data)
		if got := msg(typ, data); string(got) != string(want) {
			t.Errorf("value %d: got %s, want %s", i, got, want)
		}
	}
}

func TestRoomRelayEncodingCompatibility(t *testing.T) {
	for _, kind := range []string{"state", "msg"} {
		for _, data := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(` [ 1, true, "<>&\u2028" ] `), json.RawMessage(`{"nested":{"x":1}}`)} {
			h := NewHub(nil, nil)
			sender := &Client{hub: h, playerID: "p\"<>&\n"}
			receiver := &Client{hub: h, send: make(chan []byte, 1)}
			room := &Room{members: map[string]*Client{sender.playerID: sender, "receiver": receiver}, states: map[string]json.RawMessage{}}
			sender.room = room
			field := "state"
			if kind == "state" {
				h.handleRoomState(sender, data)
			} else {
				field = "data"
				h.handleRoomMsg(sender, data)
			}
			got := <-receiver.send
			receiver.releaseQueued(got)
			want := previousMessageEncoding("room."+kind, map[string]any{"player_id": sender.playerID, field: data})
			if string(got) != string(want) {
				t.Fatalf("%s got %s, want %s", kind, got, want)
			}
			if h.queuedBytes.Load() != 0 {
				t.Fatal("queued bytes leaked")
			}
		}
	}
}
