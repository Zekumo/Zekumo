package realtime

import (
	"encoding/json"
	"regexp"
	"sort"
	"time"
)

const maxQueuedPerGame = 128
const matchWait = 120 * time.Second

var matchPoolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type MatchTicket struct {
	ID        string    `json:"id"`
	Pool      string    `json:"pool"`
	Players   int       `json:"players"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	timer     *time.Timer
}

func (h *Hub) cancelMatchLocked(c *Client) {
	if c.match == nil {
		return
	}
	if c.match.timer != nil {
		c.match.timer.Stop()
	}
	c.match = nil
}

func (h *Hub) handleMatchJoin(c *Client, data json.RawMessage) {
	var req struct {
		Pool    string `json:"pool"`
		Players int    `json:"players"`
	}
	if err := decodeRoomRequest(data, &req); err != nil || !matchPoolName.MatchString(req.Pool) || req.Players < 2 || req.Players > 16 {
		c.sendError("bad_match", "pool must be 1..64 letters/digits/_/- and players must be 2..16")
		return
	}
	if c.room != nil {
		c.sendError("already_in_room", "leave your room before matchmaking")
		return
	}
	if c.match != nil {
		if c.match.Pool != req.Pool || c.match.Players != req.Players {
			c.sendError("already_queued", "cancel the current ticket first")
			return
		}
		c.reply("match.queued", c.match)
		return
	}
	g := h.space(c.gameID)
	queued := 0
	for _, peer := range g.clients {
		if peer.match != nil {
			queued++
		}
	}
	if queued >= maxQueuedPerGame {
		c.sendError("match_capacity", "game matchmaking queue is full")
		return
	}
	if len(g.rooms) >= maxRoomsPerGame {
		c.sendError("room_limit", "game room capacity reached")
		return
	}
	now := time.Now()
	ticket := &MatchTicket{ID: randomID("mt_"), Pool: req.Pool, Players: req.Players, CreatedAt: now, ExpiresAt: now.Add(matchWait)}
	c.match = ticket
	ticket.timer = time.AfterFunc(matchWait, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if c.match == ticket {
			h.cancelMatchLocked(c)
			c.enqueue(msg("match.expired", map[string]string{"ticket_id": ticket.ID}))
		}
	})
	c.reply("match.queued", ticket)
	candidates := []*Client{}
	for _, peer := range g.clients {
		t := peer.match
		if t != nil && t.Pool == req.Pool && t.Players == req.Players && peer.room == nil && t.ExpiresAt.After(now) {
			peer.sendMu.Lock()
			live := !peer.closed
			peer.sendMu.Unlock()
			if live {
				candidates = append(candidates, peer)
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].match, candidates[j].match
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	if len(candidates) < req.Players {
		return
	}
	selected := []*Client{}
	// Try another oldest seed if a blocked pair would otherwise stall compatible
	// players behind it. Work stays bounded by the 128-ticket game queue.
	for seed := 0; seed < len(candidates); seed++ {
		selected = selected[:0]
		for _, candidate := range candidates[seed:] {
			compatible := true
			for _, peer := range selected {
				if !candidate.canSee(peer.playerID) || !peer.canSee(candidate.playerID) {
					compatible = false
					break
				}
			}
			if compatible {
				selected = append(selected, candidate)
			}
			if len(selected) == req.Players {
				break
			}
		}
		if len(selected) == req.Players {
			break
		}
	}
	if len(selected) < req.Players {
		return
	}
	candidates = selected
	room := &Room{RequireReady: true, MinPlayers: req.Players, ID: randomID("r_"), Name: req.Pool, OwnerID: candidates[0].playerID, MaxPlayers: req.Players, Locked: true,
		members: map[string]*Client{}, states: map[string]json.RawMessage{}, SessionState: "lobby"}
	g.rooms[room.ID] = room
	for _, peer := range candidates {
		room.members[peer.playerID] = peer
		peer.room = room
	}
	for _, peer := range candidates {
		id := peer.match.ID
		h.cancelMatchLocked(peer)
		peer.enqueue(msg("match.found", map[string]any{"ticket_id": id, "room": room.snapshotFor(peer)}))
	}
}

func (h *Hub) handleMatchCancel(c *Client) {
	h.cancelMatchLocked(c)
	c.reply("match.cancelled", struct{}{})
}

func (h *Hub) handleMatchStatus(c *Client) {
	status := "idle"
	if c.match != nil {
		status = "queued"
	}
	var room *Room
	if c.room != nil {
		status = "in_room"
		snapshot := c.room.snapshotFor(c)
		room = &snapshot
	}
	c.reply("match.status", map[string]any{"status": status, "ticket": c.match, "room": room})
}

// Session transitions are owner-authorized room lifecycle events. They do not
// attest game results or award rewards; client-owned rooms are not authoritative.
func (h *Hub) handleSession(c *Client, start bool) {
	room := h.managedRoom(c, true)
	if room == nil {
		return
	}
	state := room.SessionState
	if state == "" {
		state = "lobby"
	}
	if start {
		if state != "lobby" {
			c.sendError("invalid_session_state", "only lobby sessions can start")
			return
		}
		if len(room.members) < room.MinPlayers {
			c.sendError("not_enough_players", "room has fewer than its minimum players")
			return
		}
		if room.RequireReady {
			for id := range room.members {
				if !room.ready[id] {
					c.sendError("players_not_ready", "every current member must mark ready")
					return
				}
			}
		}
		room.SessionState = "running"
		room.Locked = true
	} else {
		if state != "running" {
			c.sendError("invalid_session_state", "only running sessions can finish")
			return
		}
		room.SessionState = "finished"
	}
	for _, peer := range room.members {
		if peer != c {
			peer.enqueue(msg("room.session", room.snapshotFor(peer)))
		}
	}
	c.reply("room.session", room.snapshotFor(c))
}

func (r *Room) sessionState() string {
	if r.SessionState == "" {
		return "lobby"
	}
	return r.SessionState
}

// handleReady changes only the calling player's readiness in a lobby.
func (h *Hub) handleReady(c *Client, data json.RawMessage) {
	room := h.managedRoom(c, false)
	if room == nil {
		return
	}
	if room.sessionState() != "lobby" {
		c.sendError("invalid_session_state", "readiness can only change in a lobby")
		return
	}
	var req struct {
		Ready *bool `json:"ready"`
	}
	if err := decodeRoomRequest(data, &req); err != nil || req.Ready == nil {
		c.sendError("bad_request", "ready must be a boolean")
		return
	}
	if room.ready == nil {
		room.ready = map[string]bool{}
	}
	room.ready[c.playerID] = *req.Ready
	for _, peer := range room.members {
		if peer != c {
			peer.enqueue(msg("room.ready", room.snapshotFor(peer)))
		}
	}
	c.reply("room.ready", room.snapshotFor(c))
}

func (r *Room) readyCount() int {
	count := 0
	for id := range r.members {
		if r.ready[id] {
			count++
		}
	}
	return count
}
