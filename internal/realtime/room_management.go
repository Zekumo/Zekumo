package realtime

import "encoding/json"

// managedRoom verifies the live connection as well as the game-scoped room.
// All room handlers run with h.mu held by dispatch.
func (h *Hub) managedRoom(c *Client, ownerOnly bool) *Room {
	r := c.room
	g := h.games[c.gameID]
	if r == nil || g == nil || g.rooms[r.ID] != r || r.members[c.playerID] != c {
		c.sendError("not_in_room", "you are not in a room")
		return nil
	}
	if ownerOnly && r.OwnerID != c.playerID {
		c.sendError("not_room_owner", "only the room owner can perform this action")
		return nil
	}
	return r
}

type updateRoomReq struct {
	Name       *string         `json:"name"`
	MaxPlayers *int            `json:"max_players"`
	Meta       json.RawMessage `json:"meta"`
	Locked     *bool           `json:"locked"`
}

func (h *Hub) handleRoomUpdate(c *Client, data json.RawMessage) {
	r := h.managedRoom(c, true)
	if r == nil {
		return
	}
	var req updateRoomReq
	if err := decodeRoomRequest(data, &req); err != nil {
		c.sendError("bad_json", "room.update requires a valid settings object")
		return
	}
	// Validate the entire patch before changing any field.
	if req.Name != nil && len(*req.Name) > maxRoomNameBytes {
		c.sendError("invalid_name", "room name exceeds 128 bytes")
		return
	}
	if req.MaxPlayers != nil && (*req.MaxPlayers < 1 || *req.MaxPlayers > maxRoomPlayers || *req.MaxPlayers < len(r.members)) {
		c.sendError("invalid_max_players", "max_players must be 1 to 200 and at least the current member count")
		return
	}
	if len(req.Meta) > maxRoomMetaBytes {
		c.sendError("invalid_meta", "room metadata exceeds 8 KiB")
		return
	}
	if req.Name != nil {
		r.Name = *req.Name
	}
	if req.MaxPlayers != nil {
		r.MaxPlayers = *req.MaxPlayers
	}
	if req.Meta != nil {
		// Copy the payload so a caller cannot mutate the room after dispatch.
		r.Meta = append(json.RawMessage(nil), req.Meta...)
	}
	if req.Locked != nil {
		r.Locked = *req.Locked
	}
	payload := msg("room.updated", r.snapshot())
	for _, member := range r.members {
		member.enqueue(payload)
	}
}

type roomTargetReq struct {
	PlayerID string `json:"player_id"`
}

func (h *Hub) handleRoomKick(c *Client, data json.RawMessage) {
	r := h.managedRoom(c, true)
	if r == nil {
		return
	}
	var req roomTargetReq
	if err := decodeRoomRequest(data, &req); err != nil {
		c.sendError("bad_json", "room.kick requires a valid player_id object")
		return
	}
	if req.PlayerID == "" || req.PlayerID == c.playerID {
		c.sendError("invalid_target", "choose another room member")
		return
	}
	target := r.members[req.PlayerID]
	if target == nil {
		c.sendError("member_not_found", "player is not in this room")
		return
	}
	h.leaveRoomLocked(target)
	target.enqueue(msg("room.kicked", map[string]string{"room_id": r.ID, "reason": "removed_by_owner"}))
	c.enqueue(msg("room.kick_ok", map[string]string{"player_id": req.PlayerID, "room_id": r.ID}))
}

func (h *Hub) handleRoomTransfer(c *Client, data json.RawMessage) {
	r := h.managedRoom(c, true)
	if r == nil {
		return
	}
	var req roomTargetReq
	if err := decodeRoomRequest(data, &req); err != nil {
		c.sendError("bad_json", "room.transfer requires a valid player_id object")
		return
	}
	if req.PlayerID == "" {
		c.sendError("invalid_target", "player_id is required")
		return
	}
	if r.members[req.PlayerID] == nil {
		c.sendError("member_not_found", "player is not in this room")
		return
	}
	r.OwnerID = req.PlayerID
	payload := msg("room.owner_changed", map[string]string{"owner_id": r.OwnerID})
	for _, member := range r.members {
		member.enqueue(payload)
	}
	c.enqueue(msg("room.transferred", r.snapshot()))
}

func (h *Hub) handleRoomGet(c *Client, data json.RawMessage) {
	var req struct{}
	if err := decodeRoomRequest(data, &req); err != nil {
		c.sendError("bad_json", "room.get requires an empty object")
		return
	}
	if r := h.managedRoom(c, false); r != nil {
		c.enqueue(msg("room.info", r.snapshot()))
	}
}
