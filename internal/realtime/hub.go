// Package realtime is the WebSocket gateway: one connection per player,
// multiplexing room sync (create/join/state/messages) and chat channels.
// Rooms live in process memory — a single server instance owns all rooms.
package realtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"zekumo/internal/auth"
	"zekumo/internal/chat"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Mini-game clients (WeChat webviews, itch embeds) come from arbitrary origins.
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

func msg(typ string, data any) []byte {
	raw, _ := json.Marshal(data)
	out, _ := json.Marshal(Envelope{Type: typ, Data: raw})
	return out
}

type Member struct {
	PlayerID string          `json:"player_id"`
	Nickname string          `json:"nickname"`
	State    json.RawMessage `json:"state,omitempty"`
}

type Room struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	OwnerID    string          `json:"owner_id"`
	MaxPlayers int             `json:"max_players"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	Members    []Member        `json:"members"`

	members map[string]*Client
	states  map[string]json.RawMessage
}

func (r *Room) snapshot() Room {
	out := *r
	out.Members = make([]Member, 0, len(r.members))
	for id, c := range r.members {
		out.Members = append(out.Members, Member{PlayerID: id, Nickname: c.nickname, State: r.states[id]})
	}
	return out
}

// gameSpace isolates one game's clients, rooms and chat subscriptions.
type gameSpace struct {
	clients  map[string]*Client            // playerID -> connection
	rooms    map[string]*Room              // roomID -> room
	channels map[string]map[*Client]struct{} // chat channel -> subscribers
}

type Hub struct {
	mu     sync.Mutex
	games  map[string]*gameSpace
	Chat   *chat.Service
	Issuer *auth.TokenIssuer
}

func NewHub(chatSvc *chat.Service, issuer *auth.TokenIssuer) *Hub {
	return &Hub{games: map[string]*gameSpace{}, Chat: chatSvc, Issuer: issuer}
}

func (h *Hub) space(gameID string) *gameSpace {
	g, ok := h.games[gameID]
	if !ok {
		g = &gameSpace{
			clients:  map[string]*Client{},
			rooms:    map[string]*Room{},
			channels: map[string]map[*Client]struct{}{},
		}
		h.games[gameID] = g
	}
	return g
}

// OnlineCount reports connected players for a game (used by admin stats).
func (h *Hub) OnlineCount(gameID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if g, ok := h.games[gameID]; ok {
		return len(g.clients)
	}
	return 0
}

// IsOnline reports whether a specific player is currently connected via WebSocket.
func (h *Hub) IsOnline(gameID, playerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if g, ok := h.games[gameID]; ok {
		_, ok := g.clients[playerID]
		return ok
	}
	return false
}

// ServeWS handles GET /v1/ws?token=... — the auth middleware has already
// verified the token and stored claims in the context.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "upgrade_failed", err.Error())
		return
	}
	client := &Client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 256),
		playerID: claims.Subject,
		gameID:   claims.GameID,
		nickname: claims.Nickname,
		subs:     map[string]struct{}{},
	}

	h.mu.Lock()
	g := h.space(client.gameID)
	if old, ok := g.clients[client.playerID]; ok {
		h.removeClientLocked(old) // one live connection per player
		old.close(websocket.ClosePolicyViolation, "replaced by a new connection")
	}
	g.clients[client.playerID] = client
	h.mu.Unlock()

	client.enqueue(msg("welcome", map[string]string{
		"player_id": client.playerID,
		"nickname":  client.nickname,
	}))
	go client.writePump()
	go client.readPump()
}

// removeClientLocked detaches a client from its room and subscriptions. Caller holds h.mu.
func (h *Hub) removeClientLocked(c *Client) {
	g := h.space(c.gameID)
	if g.clients[c.playerID] == c {
		delete(g.clients, c.playerID)
	}
	for ch := range c.subs {
		if subs, ok := g.channels[ch]; ok {
			delete(subs, c)
			if len(subs) == 0 {
				delete(g.channels, ch)
			}
		}
	}
	h.leaveRoomLocked(c)
}

func (h *Hub) leaveRoomLocked(c *Client) {
	room := c.room
	if room == nil {
		return
	}
	c.room = nil
	delete(room.members, c.playerID)
	delete(room.states, c.playerID)
	g := h.space(c.gameID)
	if len(room.members) == 0 {
		delete(g.rooms, room.ID)
		return
	}
	if room.OwnerID == c.playerID { // promote an arbitrary remaining member
		for id := range room.members {
			room.OwnerID = id
			break
		}
	}
	payload := msg("room.member_left", map[string]string{
		"player_id": c.playerID,
		"new_owner": room.OwnerID,
	})
	for _, m := range room.members {
		m.enqueue(payload)
	}
}

func randomID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// --- message handlers, all called with h.mu held ---

type createRoomReq struct {
	Name       string          `json:"name"`
	MaxPlayers int             `json:"max_players"`
	Meta       json.RawMessage `json:"meta"`
}

func (h *Hub) handleRoomCreate(c *Client, data json.RawMessage) {
	var req createRoomReq
	_ = json.Unmarshal(data, &req)
	if c.room != nil {
		c.sendError("already_in_room", "leave your current room first")
		return
	}
	if req.MaxPlayers <= 0 || req.MaxPlayers > 200 {
		req.MaxPlayers = 20
	}
	g := h.space(c.gameID)
	room := &Room{
		ID:         randomID("r_"),
		Name:       req.Name,
		OwnerID:    c.playerID,
		MaxPlayers: req.MaxPlayers,
		Meta:       req.Meta,
		members:    map[string]*Client{c.playerID: c},
		states:     map[string]json.RawMessage{},
	}
	g.rooms[room.ID] = room
	c.room = room
	c.enqueue(msg("room.created", room.snapshot()))
}

func (h *Hub) handleRoomJoin(c *Client, data json.RawMessage) {
	var req struct {
		RoomID string `json:"room_id"`
	}
	_ = json.Unmarshal(data, &req)
	if c.room != nil {
		c.sendError("already_in_room", "leave your current room first")
		return
	}
	room, ok := h.space(c.gameID).rooms[req.RoomID]
	if !ok {
		c.sendError("room_not_found", "no room with this id")
		return
	}
	if len(room.members) >= room.MaxPlayers {
		c.sendError("room_full", "room is full")
		return
	}
	room.members[c.playerID] = c
	c.room = room
	joined := msg("room.member_joined", Member{PlayerID: c.playerID, Nickname: c.nickname})
	for id, m := range room.members {
		if id != c.playerID {
			m.enqueue(joined)
		}
	}
	c.enqueue(msg("room.joined", room.snapshot()))
}

func (h *Hub) handleRoomLeave(c *Client) {
	if c.room == nil {
		c.sendError("not_in_room", "you are not in a room")
		return
	}
	h.leaveRoomLocked(c)
	c.enqueue(msg("room.left", struct{}{}))
}

func (h *Hub) handleRoomList(c *Client) {
	g := h.space(c.gameID)
	rooms := make([]Room, 0, len(g.rooms))
	for _, r := range g.rooms {
		rooms = append(rooms, r.snapshot())
	}
	c.enqueue(msg("room.list", map[string]any{"rooms": rooms}))
}

// handleRoomState stores the sender's state blob and broadcasts it to roommates.
// The server relays without interpreting — game rules stay client-side.
func (h *Hub) handleRoomState(c *Client, data json.RawMessage) {
	if c.room == nil {
		c.sendError("not_in_room", "join a room before sending state")
		return
	}
	c.room.states[c.playerID] = data
	payload := msg("room.state", map[string]any{
		"player_id": c.playerID,
		"state":     data,
	})
	for id, m := range c.room.members {
		if id != c.playerID {
			m.enqueue(payload)
		}
	}
}

func (h *Hub) handleRoomMsg(c *Client, data json.RawMessage) {
	if c.room == nil {
		c.sendError("not_in_room", "join a room before sending messages")
		return
	}
	payload := msg("room.msg", map[string]any{
		"player_id": c.playerID,
		"data":      data,
	})
	for id, m := range c.room.members {
		if id != c.playerID {
			m.enqueue(payload)
		}
	}
}

func (h *Hub) handleChatSub(c *Client, data json.RawMessage, sub bool) {
	var req struct {
		Channel string `json:"channel"`
	}
	_ = json.Unmarshal(data, &req)
	if req.Channel == "" {
		c.sendError("missing_channel", "channel is required")
		return
	}
	g := h.space(c.gameID)
	if sub {
		if len(c.subs) >= 32 {
			c.sendError("too_many_channels", "at most 32 channel subscriptions")
			return
		}
		if g.channels[req.Channel] == nil {
			g.channels[req.Channel] = map[*Client]struct{}{}
		}
		g.channels[req.Channel][c] = struct{}{}
		c.subs[req.Channel] = struct{}{}
		c.enqueue(msg("chat.subbed", map[string]string{"channel": req.Channel}))
	} else {
		delete(c.subs, req.Channel)
		if subs, ok := g.channels[req.Channel]; ok {
			delete(subs, c)
			if len(subs) == 0 {
				delete(g.channels, req.Channel)
			}
		}
		c.enqueue(msg("chat.unsubbed", map[string]string{"channel": req.Channel}))
	}
}

func (h *Hub) handleChatSend(c *Client, data json.RawMessage) {
	var req struct {
		Channel string `json:"channel"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal(data, &req)
	if req.Channel == "" {
		c.sendError("missing_channel", "channel is required")
		return
	}
	content, ok := h.Chat.Prepare(req.Content)
	if !ok {
		c.sendError("bad_message", "message is empty or too long")
		return
	}
	m := repo.ChatMessage{
		GameID:     c.gameID,
		Channel:    req.Channel,
		SenderID:   c.playerID,
		SenderName: c.nickname,
		Content:    content,
		CreatedAt:  time.Now().UTC(),
	}
	h.Chat.Save(m)
	payload := msg("chat.msg", m)
	for sub := range h.space(c.gameID).channels[req.Channel] {
		sub.enqueue(payload)
	}
}

func (h *Hub) dispatch(c *Client, env Envelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch env.Type {
	case "room.create":
		h.handleRoomCreate(c, env.Data)
	case "room.join":
		h.handleRoomJoin(c, env.Data)
	case "room.leave":
		h.handleRoomLeave(c)
	case "room.list":
		h.handleRoomList(c)
	case "room.state":
		h.handleRoomState(c, env.Data)
	case "room.msg":
		h.handleRoomMsg(c, env.Data)
	case "chat.sub":
		h.handleChatSub(c, env.Data, true)
	case "chat.unsub":
		h.handleChatSub(c, env.Data, false)
	case "chat.send":
		h.handleChatSend(c, env.Data)
	case "ping":
		c.enqueue(msg("pong", struct{}{}))
	default:
		c.sendError("unknown_type", "unknown message type: "+env.Type)
	}
}

func (h *Hub) disconnect(c *Client) {
	h.mu.Lock()
	h.removeClientLocked(c)
	h.mu.Unlock()
	c.close(websocket.CloseNormalClosure, "")
	log.Printf("realtime: player %s disconnected", c.playerID)
}
