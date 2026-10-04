// Package realtime implements the WebSocket gateway. Room metadata and
// membership live in Redis; processes keep only the sockets they own and use
// room:<room_id> Pub/Sub channels for cross-instance delivery.
package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"minicloud/internal/auth"
	"minicloud/internal/chat"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	redisOperationTimeout = 750 * time.Millisecond
	presenceTTL           = 45 * time.Second
	roomTTL               = 90 * time.Second
)

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

// Room is only a wire representation. It intentionally contains no local
// connection pointers: Redis is authoritative for room state.
type Room struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	OwnerID    string          `json:"owner_id"`
	MaxPlayers int             `json:"max_players"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	Members    []Member        `json:"members"`
}

type gameSpace struct {
	clients  map[string]*Client
	channels map[string]map[*Client]struct{}
}

type Hub struct {
	mu     sync.Mutex
	games  map[string]*gameSpace
	Chat   *chat.Service
	Issuer *auth.TokenIssuer
	RDB    *redis.Client
	Blocks repo.PlayerBlocks

	instanceID string
	redisReady atomic.Bool
}

type roomEvent struct {
	Origin   string          `json:"origin"`
	GameID   string          `json:"game_id"`
	RoomID   string          `json:"room_id"`
	SenderID string          `json:"sender_id,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

type kickEvent struct {
	Origin, GameID, PlayerID, KeepConnID string
}

var createRoomScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], 'id', ARGV[1], 'name', ARGV[2],
  'owner_id', ARGV[3], 'max_players', ARGV[4], 'meta', ARGV[5])
redis.call('HSET', KEYS[2], ARGV[3], ARGV[6])
redis.call('SADD', KEYS[3], ARGV[1])

redis.call('EXPIRE', KEYS[1], ARGV[7])
redis.call('EXPIRE', KEYS[2], ARGV[7])
redis.call('EXPIRE', KEYS[3], ARGV[7])
return 1
`)

var joinRoomScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return -1 end
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])

  redis.call('EXPIRE', KEYS[1], ARGV[3])

  redis.call('EXPIRE', KEYS[2], ARGV[3])
  redis.call('EXPIRE', KEYS[3], ARGV[3])
  return 1
end
local cap = tonumber(redis.call('HGET', KEYS[1], 'max_players')) or 0
if redis.call('HLEN', KEYS[2]) >= cap then return 0 end
redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])

redis.call('EXPIRE', KEYS[1], ARGV[3])

redis.call('EXPIRE', KEYS[2], ARGV[3])
redis.call('EXPIRE', KEYS[3], ARGV[3])
return 1
`)

var leaveRoomScript = redis.NewScript(`
if redis.call('HDEL', KEYS[2], ARGV[1]) == 0 then return '__missing__' end
if redis.call('HLEN', KEYS[2]) == 0 then
  redis.call('DEL', KEYS[1], KEYS[2])
  redis.call('SREM', KEYS[3], ARGV[2])
  return '__empty__'
end
local owner = redis.call('HGET', KEYS[1], 'owner_id') or ''
if owner == ARGV[1] then
  owner = redis.call('HKEYS', KEYS[2])[1]
  redis.call('HSET', KEYS[1], 'owner_id', owner)
end
return owner
`)

var deletePresenceScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func NewHub(ctx context.Context, rdb *redis.Client, chatSvc *chat.Service, issuer *auth.TokenIssuer, blocks repo.PlayerBlocks) *Hub {
	h := &Hub{
		games: map[string]*gameSpace{}, Chat: chatSvc, Issuer: issuer, RDB: rdb,
		Blocks: blocks, instanceID: randomID("ws_"),
	}
	if rdb != nil {
		h.redisReady.Store(true) // store.Open has already pinged Redis.
		go h.runPubSub(ctx)
		go h.monitorRedis(ctx)
	}
	return h
}

func (h *Hub) space(gameID string) *gameSpace {
	g := h.games[gameID]
	if g == nil {
		g = &gameSpace{clients: map[string]*Client{}, channels: map[string]map[*Client]struct{}{}}
		h.games[gameID] = g
	}
	return g
}

func roomMetaKey(gameID, roomID string) string {
	return "minicloud:rt:{" + gameID + "}:room:" + roomID + ":meta"
}
func roomMembersKey(gameID, roomID string) string {
	return "minicloud:rt:{" + gameID + "}:room:" + roomID + ":members"
}
func gameRoomsKey(gameID string) string { return "minicloud:rt:{" + gameID + "}:rooms" }
func presenceKey(gameID, playerID string) string {
	return "minicloud:rt:{" + gameID + "}:presence:" + playerID
}
func roomChannel(roomID string) string { return "room:" + roomID }
func playerChannel(gameID, playerID string) string {
	return "player:" + gameID + ":" + playerID
}

func (h *Hub) redisContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), redisOperationTimeout)
}

func (h *Hub) monitorRedis(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, redisOperationTimeout)
			err := h.RDB.Ping(pingCtx).Err()
			cancel()
			h.redisReady.Store(err == nil)
		}
	}
}

func (h *Hub) runPubSub(ctx context.Context) {
	for ctx.Err() == nil {
		pubsub := h.RDB.PSubscribe(ctx, "room:*", "player:*")
		if _, err := pubsub.Receive(ctx); err != nil {
			h.redisReady.Store(false)
			_ = pubsub.Close()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		h.redisReady.Store(true)
		for incoming := range pubsub.Channel(redis.WithChannelSize(1024)) {
			if strings.HasPrefix(incoming.Channel, "room:") {
				h.receiveRoomEvent(incoming.Payload)
			} else if strings.HasPrefix(incoming.Channel, "player:") {
				h.receiveKickEvent(incoming.Payload)
			}
			if ctx.Err() != nil {
				_ = pubsub.Close()
				return
			}
		}
		h.redisReady.Store(false)
		_ = pubsub.Close()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (h *Hub) receiveRoomEvent(raw string) {
	var event roomEvent
	if json.Unmarshal([]byte(raw), &event) != nil || event.Origin == h.instanceID {
		return
	}
	h.localRoomBroadcast(event.GameID, event.RoomID, event.SenderID, event.Payload)
}

func (h *Hub) receiveKickEvent(raw string) {
	var event kickEvent
	if json.Unmarshal([]byte(raw), &event) != nil || event.Origin == h.instanceID {
		return
	}
	h.mu.Lock()
	client := h.space(event.GameID).clients[event.PlayerID]
	h.mu.Unlock()
	if client != nil && client.connID != event.KeepConnID {
		client.close(websocket.ClosePolicyViolation, "replaced by a new connection")
	}
}

func (h *Hub) available(ctx context.Context) bool {
	if h.RDB == nil || !h.redisReady.Load() {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, redisOperationTimeout)
	defer cancel()
	if err := h.RDB.Ping(pingCtx).Err(); err != nil {
		h.redisReady.Store(false)
		return false
	}
	return true
}

// OnlineCount reports recently heartbeating players across all instances.
func (h *Hub) OnlineCount(gameID string) int {
	if h.RDB != nil && h.redisReady.Load() {
		ctx, cancel := h.redisContext()
		defer cancel()
		var cursor uint64
		count := 0
		for {
			keys, next, err := h.RDB.Scan(ctx, cursor, presenceKey(gameID, "*"), 200).Result()
			if err != nil {
				h.redisReady.Store(false)
				break
			}
			count += len(keys)
			cursor = next
			if cursor == 0 {
				return count
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.space(gameID).clients)
}

// IsOnline reports whether the player's heartbeat is current on any instance.
func (h *Hub) IsOnline(gameID, playerID string) bool {
	h.mu.Lock()
	_, local := h.space(gameID).clients[playerID]
	h.mu.Unlock()
	if local {
		return true
	}
	if h.RDB == nil || !h.redisReady.Load() {
		return false
	}
	ctx, cancel := h.redisContext()
	defer cancel()
	n, err := h.RDB.Exists(ctx, presenceKey(gameID, playerID)).Result()
	if err != nil {
		h.redisReady.Store(false)
		return false
	}
	return n > 0
}

func (h *Hub) setPresence(c *Client) error {
	ctx, cancel := h.redisContext()
	defer cancel()
	err := h.RDB.Set(ctx, presenceKey(c.gameID, c.playerID), c.connID, presenceTTL).Err()
	if err != nil {
		h.redisReady.Store(false)
	}
	return err
}

func (h *Hub) touchPresence(c *Client) {
	if h.RDB == nil {
		return
	}
	ctx, cancel := h.redisContext()
	defer cancel()
	value, err := h.RDB.Get(ctx, presenceKey(c.gameID, c.playerID)).Result()
	if err != nil || value != c.connID {
		if err != nil && !errors.Is(err, redis.Nil) {
			h.redisReady.Store(false)
		}
		return
	}
	if err := h.RDB.Expire(ctx, presenceKey(c.gameID, c.playerID), presenceTTL).Err(); err != nil {
		h.redisReady.Store(false)
	}
	h.mu.Lock()
	roomID := c.roomID
	h.mu.Unlock()
	if roomID != "" {
		pipe := h.RDB.Pipeline()
		pipe.Expire(ctx, roomMetaKey(c.gameID, roomID), roomTTL)
		pipe.Expire(ctx, roomMembersKey(c.gameID, roomID), roomTTL)
		pipe.Expire(ctx, gameRoomsKey(c.gameID), roomTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			h.redisReady.Store(false)
		}
	}
}

func (h *Hub) clearPresence(c *Client) {
	if h.RDB == nil {
		return
	}
	ctx, cancel := h.redisContext()
	defer cancel()
	if err := deletePresenceScript.Run(ctx, h.RDB,
		[]string{presenceKey(c.gameID, c.playerID)}, c.connID).Err(); err != nil {
		h.redisReady.Store(false)
	}
}

// ServeWS refuses new sessions while Redis is down. Existing sockets remain
// open and continue local delivery while the subscriber reconnects.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	if !h.available(r.Context()) {
		httpx.Error(w, http.StatusServiceUnavailable, "realtime_unavailable",
			"realtime is temporarily unavailable while Redis reconnects")
		return
	}
	claims := auth.ClaimsFrom(r.Context())
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "upgrade_failed", err.Error())
		return
	}
	client := &Client{
		hub: h, conn: conn, send: make(chan []byte, 256), connID: randomID("c_"),
		playerID: claims.Subject, gameID: claims.GameID, nickname: claims.Nickname,
		subs: map[string]struct{}{},
	}
	if err := h.setPresence(client); err != nil {
		client.close(websocket.CloseTryAgainLater, "realtime storage unavailable")
		return
	}
	h.mu.Lock()
	g := h.space(client.gameID)
	old := g.clients[client.playerID]
	g.clients[client.playerID] = client
	h.mu.Unlock()
	if old != nil {
		old.close(websocket.ClosePolicyViolation, "replaced by a new connection")
	}
	h.publishKick(client)
	client.enqueue(msg("welcome", map[string]string{
		"player_id": client.playerID, "nickname": client.nickname,
	}))
	go client.writePump()
	go client.readPump()
}

func (h *Hub) publishKick(c *Client) {
	event, _ := json.Marshal(kickEvent{
		Origin: h.instanceID, GameID: c.gameID, PlayerID: c.playerID, KeepConnID: c.connID,
	})
	ctx, cancel := h.redisContext()
	defer cancel()
	if err := h.RDB.Publish(ctx, playerChannel(c.gameID, c.playerID), event).Err(); err != nil {
		h.redisReady.Store(false)
	}
}

func (h *Hub) removeClientLocal(c *Client) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	g := h.space(c.gameID)
	if g.clients[c.playerID] == c {
		delete(g.clients, c.playerID)
	}
	for channel := range c.subs {
		if subs := g.channels[channel]; subs != nil {
			delete(subs, c)
			if len(subs) == 0 {
				delete(g.channels, channel)
			}
		}
	}
	roomID := c.roomID
	c.roomID = ""
	return roomID
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

type createRoomReq struct {
	Name       string          `json:"name"`
	MaxPlayers int             `json:"max_players"`
	Meta       json.RawMessage `json:"meta"`
}

func (h *Hub) handleRoomCreate(c *Client, data json.RawMessage) {
	h.mu.Lock()
	inRoom := c.roomID != ""
	h.mu.Unlock()
	if inRoom {
		c.sendError("already_in_room", "leave your current room first")
		return
	}
	var req createRoomReq
	_ = json.Unmarshal(data, &req)
	if req.MaxPlayers <= 0 || req.MaxPlayers > 200 {
		req.MaxPlayers = 20
	}
	if len(req.Meta) == 0 {
		req.Meta = json.RawMessage("null")
	}
	member := Member{PlayerID: c.playerID, Nickname: c.nickname}
	memberRaw, _ := json.Marshal(member)
	var roomID string
	for attempt := 0; attempt < 3; attempt++ {
		roomID = randomID("r_")
		ctx, cancel := h.redisContext()
		created, err := createRoomScript.Run(ctx, h.RDB,
			[]string{roomMetaKey(c.gameID, roomID), roomMembersKey(c.gameID, roomID), gameRoomsKey(c.gameID)},
			roomID, req.Name, c.playerID, req.MaxPlayers, string(req.Meta), string(memberRaw), int(roomTTL.Seconds())).Int()
		cancel()
		if err != nil {
			h.redisReady.Store(false)
			c.sendError("realtime_unavailable", "room storage is temporarily unavailable")
			return
		}
		if created == 1 {
			break
		}
		roomID = ""
	}
	if roomID == "" {
		c.sendError("room_create_failed", "could not allocate a room id")
		return
	}
	h.mu.Lock()
	c.roomID = roomID
	h.mu.Unlock()
	c.enqueue(msg("room.created", Room{
		ID: roomID, Name: req.Name, OwnerID: c.playerID, MaxPlayers: req.MaxPlayers,
		Meta: req.Meta, Members: []Member{member},
	}))
}

func (h *Hub) handleRoomJoin(c *Client, data json.RawMessage) {
	h.mu.Lock()
	inRoom := c.roomID != ""
	h.mu.Unlock()
	if inRoom {
		c.sendError("already_in_room", "leave your current room first")
		return
	}
	var req struct {
		RoomID string `json:"room_id"`
	}
	_ = json.Unmarshal(data, &req)
	if req.RoomID == "" {
		c.sendError("room_not_found", "no room with this id")
		return
	}
	// Reconcile members whose presence heartbeat expired before applying the
	// capacity check. Otherwise a crashed instance could leave a full room
	// permanently unjoinable while another member keeps the room alive.
	if _, err := h.roomSnapshot(c.gameID, req.RoomID, c.playerID); err != nil {
		if errors.Is(err, redis.Nil) {
			c.sendError("room_not_found", "no room with this id")
		} else {
			h.redisReady.Store(false)
			c.sendError("realtime_unavailable", "could not load room state")
		}
		return
	}
	memberRaw, _ := json.Marshal(Member{PlayerID: c.playerID, Nickname: c.nickname})
	ctx, cancel := h.redisContext()
	result, err := joinRoomScript.Run(ctx, h.RDB,
		[]string{roomMetaKey(c.gameID, req.RoomID), roomMembersKey(c.gameID, req.RoomID), gameRoomsKey(c.gameID)},
		c.playerID, string(memberRaw), int(roomTTL.Seconds())).Int()
	cancel()
	if err != nil {
		h.redisReady.Store(false)
		c.sendError("realtime_unavailable", "room storage is temporarily unavailable")
		return
	}
	if result == -1 {
		c.sendError("room_not_found", "no room with this id")
		return
	}
	if result == 0 {
		c.sendError("room_full", "room is full")
		return
	}
	h.mu.Lock()
	c.roomID = req.RoomID
	h.mu.Unlock()
	room, err := h.roomSnapshot(c.gameID, req.RoomID, c.playerID)
	if err != nil {
		h.leaveRoom(c.gameID, req.RoomID, c.playerID, false)
		h.mu.Lock()
		c.roomID = ""
		h.mu.Unlock()
		c.sendError("realtime_unavailable", "could not load room state")
		return
	}
	h.broadcastRoom(c.gameID, req.RoomID, c.playerID,
		msg("room.member_joined", Member{PlayerID: c.playerID, Nickname: c.nickname}))
	c.enqueue(msg("room.joined", room))
}

func (h *Hub) handleRoomLeave(c *Client) {
	h.mu.Lock()
	roomID := c.roomID
	c.roomID = ""
	h.mu.Unlock()
	if roomID == "" {
		c.sendError("not_in_room", "you are not in a room")
		return
	}
	h.leaveRoom(c.gameID, roomID, c.playerID, true)
	c.enqueue(msg("room.left", struct{}{}))
}

func (h *Hub) leaveRoom(gameID, roomID, playerID string, notify bool) {
	ctx, cancel := h.redisContext()
	newOwner, err := leaveRoomScript.Run(ctx, h.RDB,
		[]string{roomMetaKey(gameID, roomID), roomMembersKey(gameID, roomID), gameRoomsKey(gameID)},
		playerID, roomID).Text()
	cancel()
	if err != nil {
		h.redisReady.Store(false)
		// Redis loss is the documented degraded mode: keep local peers
		// coherent even though the cluster-wide owner cannot be updated.
		newOwner = h.localRoomOwner(gameID, roomID)
	}
	if notify && newOwner != "" && newOwner != "__empty__" && newOwner != "__missing__" {
		h.broadcastRoom(gameID, roomID, playerID, msg("room.member_left", map[string]string{
			"player_id": playerID, "new_owner": newOwner,
		}))
	}
}

func (h *Hub) localRoomOwner(gameID, roomID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for playerID, client := range h.space(gameID).clients {
		if client.roomID == roomID {
			return playerID
		}
	}
	return ""
}

func (h *Hub) handleRoomList(c *Client) {
	ctx, cancel := h.redisContext()
	ids, err := h.RDB.SMembers(ctx, gameRoomsKey(c.gameID)).Result()
	cancel()
	if err != nil {
		h.redisReady.Store(false)
		c.sendError("realtime_unavailable", "room storage is temporarily unavailable")
		return
	}
	rooms := make([]Room, 0, len(ids))
	for _, id := range ids {
		room, err := h.roomSnapshot(c.gameID, id, c.playerID)
		if errors.Is(err, redis.Nil) {
			cleanupCtx, cleanupCancel := h.redisContext()
			_ = h.RDB.SRem(cleanupCtx, gameRoomsKey(c.gameID), id).Err()
			cleanupCancel()
			continue
		}
		if err != nil {
			h.redisReady.Store(false)
			c.sendError("realtime_unavailable", "room storage is temporarily unavailable")
			return
		}
		rooms = append(rooms, room)
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	c.enqueue(msg("room.list", map[string]any{"rooms": rooms}))
}

func (h *Hub) roomSnapshot(gameID, roomID, viewerID string) (Room, error) {
	ctx, cancel := h.redisContext()
	defer cancel()
	pipe := h.RDB.Pipeline()
	metaCmd := pipe.HGetAll(ctx, roomMetaKey(gameID, roomID))
	membersCmd := pipe.HGetAll(ctx, roomMembersKey(gameID, roomID))
	if _, err := pipe.Exec(ctx); err != nil {
		return Room{}, err
	}
	meta := metaCmd.Val()
	if len(meta) == 0 {
		return Room{}, redis.Nil
	}
	maxPlayers, err := strconv.Atoi(meta["max_players"])
	if err != nil {
		return Room{}, err
	}
	room := Room{
		ID: meta["id"], Name: meta["name"], OwnerID: meta["owner_id"],
		MaxPlayers: maxPlayers, Meta: json.RawMessage(meta["meta"]), Members: []Member{},
	}
	memberValues := membersCmd.Val()
	presencePipe := h.RDB.Pipeline()
	presence := make(map[string]*redis.IntCmd, len(memberValues))
	for playerID := range memberValues {
		presence[playerID] = presencePipe.Exists(ctx, presenceKey(gameID, playerID))
	}
	if _, err := presencePipe.Exec(ctx); err != nil {
		return Room{}, err
	}
	ownerRemoved := false
	for playerID, raw := range memberValues {
		if presence[playerID].Val() == 0 {
			h.leaveRoom(gameID, roomID, playerID, false)
			ownerRemoved = ownerRemoved || playerID == room.OwnerID
			continue
		}
		var member Member
		if err := json.Unmarshal([]byte(raw), &member); err != nil {
			return Room{}, err
		}
		if viewerID != "" && member.PlayerID != viewerID {
			blockCtx, blockCancel := h.redisContext()
			blocked, blockErr := h.Blocks.EitherBlocked(blockCtx, gameID, viewerID, member.PlayerID)
			blockCancel()
			if blockErr != nil || blocked {
				// Preserve room membership/counts while keeping the blocked
				// player's latest state private in join/list snapshots.
				member.State = nil
			}
		}
		room.Members = append(room.Members, member)
	}
	if len(room.Members) == 0 {
		return Room{}, redis.Nil
	}
	if ownerRemoved {
		ownerCtx, ownerCancel := h.redisContext()
		room.OwnerID, err = h.RDB.HGet(ownerCtx, roomMetaKey(gameID, roomID), "owner_id").Result()
		ownerCancel()
		if err != nil {
			return Room{}, err
		}
	}
	sort.Slice(room.Members, func(i, j int) bool {
		return room.Members[i].PlayerID < room.Members[j].PlayerID
	})
	return room, nil
}

func (h *Hub) handleRoomState(c *Client, data json.RawMessage) {
	h.mu.Lock()
	roomID := c.roomID
	h.mu.Unlock()
	if roomID == "" {
		c.sendError("not_in_room", "join a room before sending state")
		return
	}
	memberRaw, _ := json.Marshal(Member{PlayerID: c.playerID, Nickname: c.nickname, State: data})
	ctx, cancel := h.redisContext()
	pipe := h.RDB.Pipeline()
	pipe.HSet(ctx, roomMembersKey(c.gameID, roomID), c.playerID, memberRaw)
	pipe.Expire(ctx, roomMetaKey(c.gameID, roomID), roomTTL)
	pipe.Expire(ctx, roomMembersKey(c.gameID, roomID), roomTTL)
	pipe.Expire(ctx, gameRoomsKey(c.gameID), roomTTL)
	_, err := pipe.Exec(ctx)
	cancel()
	if err != nil {
		h.redisReady.Store(false) // local delivery below is the documented fallback.
	}
	h.broadcastRoom(c.gameID, roomID, c.playerID, msg("room.state", map[string]any{
		"player_id": c.playerID, "state": data,
	}))
}

func (h *Hub) handleRoomMsg(c *Client, data json.RawMessage) {
	h.mu.Lock()
	roomID := c.roomID
	h.mu.Unlock()
	if roomID == "" {
		c.sendError("not_in_room", "join a room before sending messages")
		return
	}
	h.broadcastRoom(c.gameID, roomID, c.playerID, msg("room.msg", map[string]any{
		"player_id": c.playerID, "data": data,
	}))
}

func (h *Hub) broadcastRoom(gameID, roomID, senderID string, payload []byte) {
	// Deliver locally first. The echoed Pub/Sub event is ignored by origin.
	h.localRoomBroadcast(gameID, roomID, senderID, payload)
	event, _ := json.Marshal(roomEvent{
		Origin: h.instanceID, GameID: gameID, RoomID: roomID,
		SenderID: senderID, Payload: json.RawMessage(payload),
	})
	ctx, cancel := h.redisContext()
	defer cancel()
	if err := h.RDB.Publish(ctx, roomChannel(roomID), event).Err(); err != nil {
		h.redisReady.Store(false)
	}
}

// localRoomBroadcast enforces blocks in both directions. On a block lookup
// failure it fails closed, preventing a privacy control from leaking messages.
func (h *Hub) localRoomBroadcast(gameID, roomID, senderID string, payload []byte) {
	h.mu.Lock()
	clients := make([]*Client, 0)
	for playerID, client := range h.space(gameID).clients {
		if client.roomID == roomID && playerID != senderID {
			clients = append(clients, client)
		}
	}
	h.mu.Unlock()
	for _, client := range clients {
		if senderID != "" {
			ctx, cancel := h.redisContext()
			blocked, err := h.Blocks.EitherBlocked(ctx, gameID, senderID, client.playerID)
			cancel()
			if err != nil || blocked {
				continue
			}
		}
		client.enqueue(payload)
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
	h.mu.Lock()
	defer h.mu.Unlock()
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
		if subscribers := g.channels[req.Channel]; subscribers != nil {
			delete(subscribers, c)
			if len(subscribers) == 0 {
				delete(g.channels, req.Channel)
			}
		}
		c.enqueue(msg("chat.unsubbed", map[string]string{"channel": req.Channel}))
	}
}

func (h *Hub) handleChatSend(c *Client, data json.RawMessage) {
	var req struct {
		Channel, Content string
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
		GameID: c.gameID, Channel: req.Channel, SenderID: c.playerID,
		SenderName: c.nickname, Content: content, CreatedAt: time.Now().UTC(),
	}
	h.Chat.Save(m)
	payload := msg("chat.msg", m)
	h.mu.Lock()
	clients := make([]*Client, 0, len(h.space(c.gameID).channels[req.Channel]))
	for client := range h.space(c.gameID).channels[req.Channel] {
		clients = append(clients, client)
	}
	h.mu.Unlock()
	for _, client := range clients {
		client.enqueue(payload)
	}
}

func (h *Hub) dispatch(c *Client, env Envelope) {
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
	roomID := h.removeClientLocal(c)
	if roomID != "" {
		h.leaveRoom(c.gameID, roomID, c.playerID, true)
	}
	h.clearPresence(c)
	c.close(websocket.CloseNormalClosure, "")
	log.Printf("realtime: player %s disconnected", c.playerID)
}
