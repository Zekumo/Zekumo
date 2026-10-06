package zekumo

import (
	"encoding/json"
	"testing"
)

func TestRoomUpdateOptionalFields(t *testing.T) {
	locked := false
	raw, err := json.Marshal(RoomUpdate{Locked: &locked, Meta: json.RawMessage("null")})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["locked"]) != "false" || string(fields["meta"]) != "null" {
		t.Fatalf("lost explicit update: %s", raw)
	}
	if _, ok := fields["name"]; ok {
		t.Fatalf("omitted name serialized: %s", raw)
	}
	if _, ok := fields["max_players"]; ok {
		t.Fatalf("omitted capacity serialized: %s", raw)
	}
}

func TestLobbyPageDoesNotExposeMemberState(t *testing.T) {
	var page RoomPage
	if err := json.Unmarshal([]byte(`{"rooms":[{"id":"r1","locked":true,"member_count":2,"max_players":8}],"total":1,"offset":0,"limit":20}`), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rooms) != 1 || !page.Rooms[0].Locked || page.Rooms[0].MemberCount != 2 || page.Limit != 20 {
		t.Fatalf("unexpected page: %+v", page)
	}
}
