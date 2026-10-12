package realtime

import (
	"encoding/json"
	"testing"
)

func TestMatchmakingAndSessionLifecycle(t *testing.T) {
	h, a, b, _ := managementFixture()
	h.leaveRoomLocked(a)
	managementEvent(t, b, "room.member_left")
	h.leaveRoomLocked(b)
	h.handleMatchJoin(a, json.RawMessage(`{"pool":"duel","players":2}`))
	managementEvent(t, a, "match.queued")
	h.handleMatchJoin(b, json.RawMessage(`{"pool":"duel","players":2}`))
	managementEvent(t, b, "match.queued")
	managementEvent(t, a, "match.found")
	managementEvent(t, b, "match.found")
	if a.match != nil || b.match != nil || a.room != b.room || !a.room.Locked || len(a.room.members) != 2 {
		t.Fatal("match not atomically assigned")
	}
	h.handleSession(b, true)
	managementError(t, b, "not_room_owner")
	h.handleSession(a, true)
	managementError(t, a, "players_not_ready")
	h.handleReady(a, json.RawMessage(`{"ready":true}`))
	managementEvent(t, a, "room.ready")
	managementEvent(t, b, "room.ready")
	h.handleReady(b, json.RawMessage(`{"ready":true}`))
	managementEvent(t, a, "room.ready")
	managementEvent(t, b, "room.ready")
	h.handleSession(a, true)
	managementEvent(t, a, "room.session")
	managementEvent(t, b, "room.session")
	if a.room.SessionState != "running" {
		t.Fatal("session not running")
	}
	h.handleSession(a, false)
	managementEvent(t, a, "room.session")
	managementEvent(t, b, "room.session")
	if a.room.SessionState != "finished" {
		t.Fatal("session not finished")
	}
	h.handleSession(a, true)
	managementError(t, a, "invalid_session_state")
	h.leaveRoomLocked(a)
	managementEvent(t, b, "room.member_left")
	h.leaveRoomLocked(b)
	h.handleMatchJoin(a, json.RawMessage(`{"pool":"solo","players":2}`))
	managementEvent(t, a, "match.queued")
	h.handleMatchCancel(a)
	managementEvent(t, a, "match.cancelled")
	if a.match != nil {
		t.Fatal("cancel retained ticket")
	}
	h.handleMatchJoin(a, json.RawMessage(`{"pool":"solo","players":2}`))
	managementEvent(t, a, "match.queued")
	h.removeClientLocked(a)
	if a.match != nil {
		t.Fatal("disconnect retained ticket")
	}
}
