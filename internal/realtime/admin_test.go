package realtime

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminRoomsPaginationAndPrivacy(t *testing.T) {
	h := NewHub(nil, nil)
	g := h.space("game-a")
	g.rooms["r_b"] = &Room{ID: "r_b", Name: "B", Meta: json.RawMessage(`{"secret":"hidden"}`), members: map[string]*Client{"p": {}}, states: map[string]json.RawMessage{"p": json.RawMessage(`{"private":true}`)}}
	g.rooms["r_a"] = &Room{ID: "r_a", Name: "A", members: map[string]*Client{}}
	h.space("game-b").rooms["foreign"] = &Room{ID: "foreign"}
	r := httptest.NewRequest("GET", "/?offset=1&limit=1", nil)
	r.SetPathValue("id", "game-a")
	w := httptest.NewRecorder()
	h.AdminRooms(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	var page struct {
		Rooms  []AdminRoomSummary `json:"rooms"`
		Total  int                `json:"total"`
		Offset int                `json:"offset"`
		Limit  int                `json:"limit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Offset != 1 || page.Limit != 1 || len(page.Rooms) != 1 || page.Rooms[0].ID != "r_b" || page.Rooms[0].MemberCount != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	for _, private := range []string{"hidden", "private", "foreign", "members", "states"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("response exposes %q", private)
		}
	}
}

func TestAdminRoomsPaginationValidation(t *testing.T) {
	h := NewHub(nil, nil)
	for _, query := range []string{"offset=-1", "offset=nope", "limit=0", "limit=51", "limit=-2", "limit=abc"} {
		r := httptest.NewRequest("GET", "/?"+query, nil)
		w := httptest.NewRecorder()
		h.AdminRooms(w, r)
		if w.Code != 400 {
			t.Errorf("%s: status = %d", query, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/?offset=9223372036854775807", nil)
	w := httptest.NewRecorder()
	h.AdminRooms(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rooms":[]`) {
		t.Fatalf("empty page: %d %s", w.Code, w.Body)
	}
	if len(h.games) != 0 {
		t.Fatal("read-only listing allocated game state")
	}
}
