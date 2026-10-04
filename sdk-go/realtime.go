// realtime.go is the WebSocket client: room sync and chat over one
// connection, with reconnection handled for you.
//
//	rt := mc.Realtime()
//	rt.On("room.state", func(data json.RawMessage) {
//	    var ev struct {
//	        PlayerID string          `json:"player_id"`
//	        State    json.RawMessage `json:"state"`
//	    }
//	    json.Unmarshal(data, &ev)
//	    updatePlayer(ev.PlayerID, ev.State)
//	})
//	rt.Connect(ctx)
//	defer rt.Close()
//	rt.CreateRoom("lobby", 4, nil)
//
// Why a reconnect loop lives in the SDK: a desktop game runs for hours
// through sleep, wifi changes and VPN flaps, so a dropped socket is normal
// operation, not an error the game should have to handle. Connect returns
// once the first connection is up and then keeps it up in the background,
// backing off 1s, 2s, 4s … capped at 30s so a server restart does not get
// hammered by every client at once.
//
// One exception the loop must NOT retry: being replaced. The server allows
// one connection per player and closes the old one (code 1008) when the same
// player connects again. Reconnecting there would fight the new connection
// for the slot forever, so that closes the client for good.

package zekumo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// The server pings every 30s and drops a connection unread for 60s, so a
	// read deadline of 60s matches its liveness view exactly.
	rtReadWait  = 60 * time.Second
	rtWriteWait = 10 * time.Second

	rtBackoffMin = time.Second      // first retry, fast enough to be invisible
	rtBackoffMax = 30 * time.Second // ceiling, so a restarting server is not stampeded
)

// ErrReplaced is returned by Err after the server closed this connection
// because the same player connected from somewhere else. It is terminal: the
// client does not reconnect.
var ErrReplaced = errors.New("zekumo: connection replaced by a newer one for this player")

// Handler receives one event's payload. data is the raw "data" field, left
// undecoded so a handler only pays to parse what it cares about.
type Handler func(data json.RawMessage)

// envelope is the wire format in both directions: a type plus an opaque
// payload.
type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Realtime is a self-healing WebSocket connection. Build it with
// Client.Realtime, register handlers, then Connect.
type Realtime struct {
	c *Client

	// Dialer is exposed for a proxy or a custom TLS config; the zero value is
	// gorilla's default dialer.
	Dialer *websocket.Dialer

	mu       sync.Mutex
	conn     *websocket.Conn
	handlers map[string][]Handler
	closed   bool  // Close was called: stop reconnecting
	err      error // why the connection ended, for Err
}

// Realtime returns a realtime client for this session. Call it after logging
// in: the gateway authenticates with the player token.
func (c *Client) Realtime() *Realtime {
	return &Realtime{
		c:        c,
		handlers: map[string][]Handler{},
	}
}

// On registers a handler for one event type. Several handlers may share a
// type; all of them run, in registration order.
//
// Event types the server sends: welcome, pong, room.created, room.joined,
// room.left, room.list, room.member_joined, room.member_left, room.state,
// room.msg, chat.subbed, chat.unsubbed, chat.msg, error.
//
// Handlers run on the read goroutine, so a slow handler stalls the stream —
// hand long work to a goroutine or a channel the game loop drains.
func (r *Realtime) On(event string, h Handler) {
	r.mu.Lock()
	r.handlers[event] = append(r.handlers[event], h)
	r.mu.Unlock()
}

// wsURL turns the configured HTTP base URL into the gateway's ws:// or wss://
// URL. Deriving it here means a caller never configures the same host twice.
func (r *Realtime) wsURL() (string, error) {
	u, err := url.Parse(r.c.BaseURL())
	if err != nil {
		return "", fmt.Errorf("zekumo: bad base URL %q: %w", r.c.BaseURL(), err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("zekumo: base URL must be http or https, got %q", u.Scheme)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/ws"
	u.RawQuery = "token=" + url.QueryEscape(r.c.Token()) // the gateway takes the JWT as a query param
	return u.String(), nil
}

// Connect establishes the connection and keeps it alive until Close or ctx
// ends. It returns once the handshake succeeds, so a caller may send
// immediately; register a "welcome" handler before calling it to catch the
// server's greeting, which also fires again after every reconnect.
func (r *Realtime) Connect(ctx context.Context) error {
	conn, err := r.dial(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.conn = conn
	r.mu.Unlock()

	go r.run(ctx, conn) // read until the socket dies, then reconnect
	return nil
}

// dial opens one connection. The handshake succeeding is what makes the
// socket writable, so the caller may send as soon as this returns; the
// server's "welcome" frame arrives right after and is dispatched to handlers
// by readLoop like any other event.
func (r *Realtime) dial(ctx context.Context) (*websocket.Conn, error) {
	endpoint, err := r.wsURL()
	if err != nil {
		return nil, err
	}
	dialer := r.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	conn, res, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		// A rejected handshake carries the reason as HTTP status; surfacing it
		// distinguishes an expired token from an unreachable server.
		if res != nil {
			return nil, fmt.Errorf("zekumo: websocket handshake failed (HTTP %d): %w", res.StatusCode, err)
		}
		return nil, fmt.Errorf("zekumo: websocket dial: %w", err)
	}
	conn.SetReadLimit(64 << 10)
	return conn, nil
}

// run reads frames until the connection breaks, then reconnects unless told
// to stop. Reconnect also replays nothing: rooms are server state, so a game
// rejoins in its "welcome" handler.
func (r *Realtime) run(ctx context.Context, conn *websocket.Conn) {
	backoff := rtBackoffMin
	for {
		err := r.readLoop(conn)

		r.mu.Lock()
		stop := r.closed
		r.mu.Unlock()
		if stop || ctx.Err() != nil {
			return // Close or a cancelled context: an intentional end, not a failure
		}

		// Being replaced is terminal: the player is live on another
		// connection now, and redialing would evict them right back.
		if errors.Is(err, ErrReplaced) {
			r.fail(ErrReplaced)
			return
		}

		r.emit("reconnecting", mustJSON(map[string]any{"in_ms": backoff.Milliseconds()}))
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff *= 2; backoff > rtBackoffMax {
			backoff = rtBackoffMax
		}

		next, err := r.dial(ctx)
		if err != nil {
			continue // still down: keep backing off rather than giving up
		}
		r.mu.Lock()
		r.conn = next
		r.mu.Unlock()
		conn = next
		backoff = rtBackoffMin // a success resets the ladder
	}
}

// readLoop pumps one connection until it fails, dispatching each frame.
func (r *Realtime) readLoop(conn *websocket.Conn) error {
	// The deadline is pushed forward by every ping the server sends; gorilla
	// answers pings itself, so this handler only refreshes the clock.
	_ = conn.SetReadDeadline(time.Now().Add(rtReadWait))
	conn.SetPingHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(rtReadWait))
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// 1008 is the server evicting this connection in favour of a newer
			// one for the same player.
			if websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
				r.emit("replaced", nil)
				return ErrReplaced
			}
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(rtReadWait))

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue // the server only sends valid frames; a torn one is not worth killing the link over
		}
		r.emit(env.Type, env.Data)
	}
}

// emit runs every handler registered for an event. Handlers are copied out
// under the lock so a handler that calls On does not deadlock.
func (r *Realtime) emit(event string, data json.RawMessage) {
	r.mu.Lock()
	hs := append([]Handler(nil), r.handlers[event]...)
	r.mu.Unlock()
	for _, h := range hs {
		h(data)
	}
}

// fail records a terminal error and marks the client closed.
func (r *Realtime) fail(err error) {
	r.mu.Lock()
	r.err = err
	r.closed = true
	r.mu.Unlock()
}

// Err reports why the connection ended for good, or nil while it is live or
// cleanly closed. Check it in a "replaced" handler to tell an eviction from
// an ordinary Close.
func (r *Realtime) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close shuts the connection down and stops reconnecting.
func (r *Realtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil // idempotent: a game may Close on quit and on error
	}
	r.closed = true
	conn := r.conn
	r.mu.Unlock()

	if conn == nil {
		return nil
	}
	// Send a courteous close frame so the server frees the slot at once
	// instead of waiting for the read deadline.
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(rtWriteWait))
	return conn.Close()
}

// Send delivers one frame. It is safe to call from several goroutines, and
// returns an error if the connection is down — a game usually ignores that
// for state updates, since the next tick supersedes them anyway.
func (r *Realtime) Send(event string, data any) error {
	r.mu.Lock()
	conn, closed := r.conn, r.closed
	r.mu.Unlock()
	if closed {
		return errors.New("zekumo: realtime client is closed")
	}
	if conn == nil {
		return errors.New("zekumo: realtime client is not connected yet")
	}

	payload, err := json.Marshal(envelope{Type: event, Data: mustJSON(data)})
	if err != nil {
		return fmt.Errorf("zekumo: encode %s: %w", event, err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(rtWriteWait))
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("zekumo: send %s: %w", event, err)
	}
	return nil
}

// ---------- room and chat shorthands ----------
// Each is one Send with the server's own event name, so the protocol stays
// discoverable while call sites read as game actions.

// CreateRoom opens a room and joins it. maxPlayers 0 lets the server decide.
func (r *Realtime) CreateRoom(name string, maxPlayers int, meta any) error {
	return r.Send("room.create", map[string]any{
		"name": name, "max_players": maxPlayers, "meta": meta,
	})
}

// JoinRoom joins an existing room by id.
func (r *Realtime) JoinRoom(roomID string) error {
	return r.Send("room.join", map[string]any{"room_id": roomID})
}

// LeaveRoom leaves the current room.
func (r *Realtime) LeaveRoom() error { return r.Send("room.leave", nil) }

// ListRooms asks for the open rooms; the answer arrives as "room.list".
func (r *Realtime) ListRooms() error { return r.Send("room.list", nil) }

// SyncState publishes this player's state to the room — position, animation,
// whatever the game syncs. The server relays it to the other members as
// "room.state".
func (r *Realtime) SyncState(state any) error {
	return r.Send("room.state", map[string]any{"state": state})
}

// SendRoomMessage broadcasts a message to the room's members.
func (r *Realtime) SendRoomMessage(data any) error {
	return r.Send("room.msg", map[string]any{"data": data})
}

// SubscribeChat joins a chat channel; messages then arrive as "chat.msg".
func (r *Realtime) SubscribeChat(channel string) error {
	return r.Send("chat.sub", map[string]any{"channel": channel})
}

// UnsubscribeChat leaves a chat channel.
func (r *Realtime) UnsubscribeChat(channel string) error {
	return r.Send("chat.unsub", map[string]any{"channel": channel})
}

// SendChat posts a chat message. The server filters banned words and persists
// it before relaying.
func (r *Realtime) SendChat(channel, content string) error {
	return r.Send("chat.send", map[string]any{"channel": channel, "content": content})
}

// Ping asks for a "pong", for measuring round-trip latency. Connection
// liveness itself needs no help: the server's WebSocket pings handle that.
func (r *Realtime) Ping() error { return r.Send("ping", nil) }

// ---------- helpers ----------

// mustJSON marshals a payload, yielding nil for nil so an event with no data
// omits the field rather than sending "null".
func mustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil // an unmarshalable payload is a programming error, not a wire condition
	}
	return raw
}
