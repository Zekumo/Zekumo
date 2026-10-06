package realtime

import (
	"net/http"
	"sort"
	"strconv"

	"zekumo/internal/httpx"
)

// AdminRoomSummary deliberately excludes player state, member identities and
// arbitrary metadata. The console monitor is operational, not a state browser.
type AdminRoomSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerID     string `json:"owner_id"`
	MaxPlayers  int    `json:"max_players"`
	Locked      bool   `json:"locked"`
	MemberCount int    `json:"member_count"`
}

// AdminRooms must be registered behind admin authentication and the tenant
// ResourceGame viewer guard, which authorizes the path's game ID.
func (h *Hub) AdminRooms(w http.ResponseWriter, r *http.Request) {
	offset, limit := 0, 20
	for _, field := range []struct {
		name string
		dst  *int
	}{{"offset", &offset}, {"limit", &limit}} {
		if raw := r.URL.Query().Get(field.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || (field.name == "limit" && (n == 0 || n > 50)) {
				httpx.Error(w, http.StatusBadRequest, "invalid_pagination", "offset must be non-negative and limit must be between 1 and 50")
				return
			}
			*field.dst = n
		}
	}
	h.mu.Lock()
	rooms := make([]AdminRoomSummary, 0)
	online := 0
	if g := h.games[r.PathValue("id")]; g != nil {
		online = len(g.clients)
		for _, room := range g.rooms {
			rooms = append(rooms, AdminRoomSummary{ID: room.ID, Name: room.Name, OwnerID: room.OwnerID, MaxPlayers: room.MaxPlayers, Locked: room.Locked, MemberCount: len(room.members)})
		}
	}
	h.mu.Unlock()
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	total := len(rooms)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"rooms": rooms[start:end], "total": total, "room_count": total,
		"online_players": online, "offset": offset, "limit": limit,
	})
}
