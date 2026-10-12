package realtime

import (
	"context"
	"github.com/gorilla/websocket"
	"time"
)

// SetPlayerBlock updates an already-connected player's local visibility after
// the durable mutation succeeds. It does not remove room roster identities.
func (h *Hub) SetPlayerBlock(gameID, playerID, targetID string, blocked bool) {
	h.blockMu.Lock()
	defer h.blockMu.Unlock()
	var fresh map[string]struct{}
	if h.Blocks.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		entries, err := h.Blocks.List(ctx, gameID, playerID)
		cancel()
		if err != nil {
			h.mu.Lock()
			if g := h.games[gameID]; g != nil {
				if c := g.clients[playerID]; c != nil {
					h.removeClientLocked(c)
					c.close(websocket.CloseTryAgainLater, "block preferences unavailable")
				}
			}
			h.mu.Unlock()
			return
		}
		fresh = map[string]struct{}{}
		for _, entry := range entries {
			fresh[entry.PlayerID] = struct{}{}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	g := h.games[gameID]
	if g == nil {
		return
	}
	c := g.clients[playerID]
	if c == nil {
		return
	}
	if c.blocked == nil {
		c.blocked = map[string]struct{}{}
	}
	if fresh != nil {
		c.blocked = fresh
	} else if blocked {
		c.blocked[targetID] = struct{}{}
	} else {
		delete(c.blocked, targetID)
	}
	if c.room != nil {
		c.enqueue(msg("room.info", c.room.snapshotFor(c)))
	}
}

// canSee is called only while holding hub.mu.
func (c *Client) canSee(senderID string) bool { _, blocked := c.blocked[senderID]; return !blocked }

func (r *Room) snapshotFor(c *Client) Room {
	out := r.snapshot()
	for i := range out.Members {
		if !c.canSee(out.Members[i].PlayerID) {
			out.Members[i].State = nil
		}
	}
	return out
}

// RevokePlayer atomically removes active room/queue/channel ownership before
// closing the socket. Initialization uses the same lock, preventing a racing
// in-progress upgrade from registering after a local ban notification.
func (h *Hub) RevokePlayer(gameID, playerID string) {
	h.blockMu.Lock()
	defer h.blockMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if g := h.games[gameID]; g != nil {
		if c := g.clients[playerID]; c != nil {
			h.removeClientLocked(c)
			c.close(4003, "player banned")
		}
	}
}

// OnlinePlayerIDs is bounded by maxHubClients and contains no room payloads.
func (h *Hub) OnlinePlayerIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]string, 0)
	for _, g := range h.games {
		for id := range g.clients {
			ids = append(ids, id)
		}
	}
	return ids
}
