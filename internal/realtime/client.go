package realtime

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 30 * time.Second
	maxMessageSize = 64 << 10 // 64KB per frame
)

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	connID   string
	playerID string
	gameID   string
	nickname string

	roomID string
	subs   map[string]struct{}

	sendMu sync.Mutex
	closed bool
}

// enqueue queues a message; a client that cannot drain 256 messages is dropped.
func (c *Client) enqueue(payload []byte) {
	c.sendMu.Lock()
	if c.closed {
		c.sendMu.Unlock()
		return
	}
	select {
	case c.send <- payload:
		c.sendMu.Unlock()
	default:
		c.sendMu.Unlock()
		c.close(websocket.CloseGoingAway, "send buffer overflow")
	}
}

func (c *Client) sendError(code, message string) {
	c.enqueue(msg("error", map[string]string{"code": code, "message": message}))
}

// close detaches the client immediately and tears the socket down in the
// background. The teardown must not run inline: callers may hold the hub
// mutex, and WriteControl blocks for up to writeWait on a stalled peer —
// which is exactly the peer that triggers a close in the first place.
func (c *Client) close(code int, reason string) {
	c.sendMu.Lock()
	if c.closed {
		c.sendMu.Unlock()
		return
	}
	c.closed = true
	close(c.send)
	c.sendMu.Unlock()

	go func() {
		_ = c.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), time.Now().Add(writeWait))
		_ = c.conn.Close()
	}()
}

func (c *Client) readPump() {
	defer c.hub.disconnect(c)
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.sendError("bad_json", "messages must be JSON: {\"type\": ..., \"data\": ...}")
			continue
		}
		c.hub.dispatch(c, env)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				c.close(websocket.CloseAbnormalClosure, "write failed")
				return
			}
		case <-ticker.C:
			c.hub.touchPresence(c)
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close(websocket.CloseAbnormalClosure, "ping failed")
				return
			}
		}
	}
}
