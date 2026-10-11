package realtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRequestIDValidation(t *testing.T) {
	for _, id := range []string{"", "r1", "ABC_012-xyz", strings.Repeat("a", 64)} {
		if !validRequestID(id) {
			t.Errorf("valid ID rejected: %q", id)
		}
	}
	for _, id := range []string{"a b", "\n", "中文", "a.b", strings.Repeat("a", 65)} {
		if validRequestID(id) {
			t.Errorf("invalid ID accepted: %q", id)
		}
	}
}

func TestCorrelatedRoomReplies(t *testing.T) {
	cases := []struct{ request, data, response string }{
		{"room.list", `{}`, "room.list"},
		{"room.get", `{}`, "room.info"},
		{"room.update", `{"locked":true}`, "room.updated"},
		{"room.leave", `{}`, "room.left"},
		{"ping", `{}`, "pong"},
	}
	for _, tc := range cases {
		t.Run(tc.request, func(t *testing.T) {
			h, owner, peer, _ := managementFixture()
			h.dispatch(owner, Envelope{Type: tc.request, Data: json.RawMessage(tc.data), RequestID: "req_1"})
			got := managementEvent(t, owner, tc.response)
			if got.RequestID != "req_1" {
				t.Fatalf("reply ID = %q", got.RequestID)
			}
			for len(peer.send) > 0 {
				var event Envelope
				if err := json.Unmarshal(<-peer.send, &event); err != nil {
					t.Fatal(err)
				}
				if event.RequestID != "" {
					t.Fatalf("request ID leaked to peer: %+v", event)
				}
			}
			if owner.requestID != "" {
				t.Fatal("request ID retained after dispatch")
			}
			h.dispatch(owner, Envelope{Type: "ping"})
			if got := managementEvent(t, owner, "pong"); got.RequestID != "" {
				t.Fatal("legacy reply inherited old ID")
			}
		})
	}
}

func TestCorrelatedErrorsAreIsolated(t *testing.T) {
	h, owner, peer, _ := managementFixture()
	h.dispatch(peer, Envelope{Type: "room.update", Data: json.RawMessage(`{}`), RequestID: "peer_request"})
	if got := managementEvent(t, peer, "error"); got.RequestID != "peer_request" {
		t.Fatalf("error ID = %q", got.RequestID)
	}
	managementQuiet(t, owner)
	h.dispatch(owner, Envelope{Type: "unknown", RequestID: "unknown_request"})
	if got := managementEvent(t, owner, "error"); got.RequestID != "unknown_request" {
		t.Fatal("unknown operation error lost ID")
	}
	owner.rateAt, owner.tokens = time.Now().Add(time.Hour), 0
	h.dispatch(owner, Envelope{Type: "ping", RequestID: "rate_request"})
	if got := managementEvent(t, owner, "error"); got.RequestID != "rate_request" {
		t.Fatal("rate-limit error lost ID")
	}
	h.dispatch(peer, Envelope{Type: "room.update", Data: json.RawMessage(`{"locked":true}`), RequestID: strings.Repeat("x", 65)})
	if got := managementEvent(t, peer, "error"); got.RequestID != "" {
		t.Fatal("oversized ID reflected")
	}
}

func TestCorrelatedTransferAndKickBroadcasts(t *testing.T) {
	h, owner, peer, _ := managementFixture()
	h.dispatch(owner, Envelope{Type: "room.transfer", Data: json.RawMessage(`{"player_id":"peer"}`), RequestID: "transfer"})
	for _, c := range []*Client{owner, peer} {
		if got := managementEvent(t, c, "room.owner_changed"); got.RequestID != "" {
			t.Fatal("broadcast has ID")
		}
	}
	if got := managementEvent(t, owner, "room.transferred"); got.RequestID != "transfer" {
		t.Fatal("transfer reply lost ID")
	}
	h.dispatch(peer, Envelope{Type: "room.kick", Data: json.RawMessage(`{"player_id":"owner"}`), RequestID: "kick"})
	if got := managementEvent(t, peer, "room.member_left"); got.RequestID != "" {
		t.Fatal("member-left broadcast has ID")
	}
	if got := managementEvent(t, peer, "room.kick_ok"); got.RequestID != "kick" {
		t.Fatal("kick reply lost ID")
	}
	if got := managementEvent(t, owner, "room.kicked"); got.RequestID != "" {
		t.Fatal("kick event leaked requester ID")
	}
}
