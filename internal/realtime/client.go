package realtime

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait            = 10 * time.Second
	pongWait             = 60 * time.Second
	pingPeriod           = 30 * time.Second
	maxClientQueuedBytes = 4 << 20
	maxHubQueuedBytes    = 64 << 20
	maxMessageSize       = 64 << 10 // 64KB per frame
)

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	playerID string
	gameID   string
	nickname string

	// rate fields are protected by hub.mu.
	rateAt time.Time
	tokens float64

	room *Room
	subs map[string]struct{}

	queuedBytes atomic.Int64
	sendMu      sync.Mutex
	closed      bool
}

// allowMessage bounds per-connection fanout work (60/s with a 120-message burst).
// Caller holds hub.mu; protocol ping messages share the budget.
func (c *Client) allowMessage(now time.Time) bool {
	if c.rateAt.IsZero() {
		c.rateAt = now
		c.tokens = 120
	}
	elapsed := now.Sub(c.rateAt).Seconds()
	if elapsed > 0 {
		c.tokens += elapsed * 60
		c.rateAt = now
	}
	if c.tokens > 120 {
		c.tokens = 120
	}
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

// enqueue bounds queued messages and queued + in-flight bytes, dropping slow clients.
func (c *Client) enqueue(payload []byte) {
	c.sendMu.Lock()
	if c.closed {
		c.sendMu.Unlock()
		return
	}
	n := int64(len(payload))
	local := c.queuedBytes.Add(n)
	global := int64(0)
	if c.hub != nil {
		global = c.hub.queuedBytes.Add(n)
	}
	if local > maxClientQueuedBytes || global > maxHubQueuedBytes {
		c.releaseQueued(payload)
		c.sendMu.Unlock()
		c.close(websocket.CloseGoingAway, "send byte budget exceeded")
		return
	}
	select {
	case c.send <- payload:
		c.sendMu.Unlock()
	default:
		c.releaseQueued(payload)
		c.sendMu.Unlock()
		c.close(websocket.CloseGoingAway, "send buffer overflow")
	}
}

func (c *Client) releaseQueued(payload []byte) {
	n := int64(len(payload))
	c.queuedBytes.Add(-n)
	if c.hub != nil {
		c.hub.queuedBytes.Add(-n)
	}
}

func (c *Client) sendError(code, message string) {
	c.enqueue(msg("error", map[string]string{"code": code, "message": message}))
}

// close stops enqueueing and discards queued messages immediately. Socket teardown
// runs in the background; readPump then detaches membership. Teardown must not run inline: callers may hold the hub
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
	for payload := range c.send {
		c.releaseQueued(payload)
	}
	c.sendMu.Unlock()

	if c.conn == nil {
		return
	}
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
			err := c.conn.WriteMessage(websocket.TextMessage, payload)
			c.releaseQueued(payload) // Include in-flight writes in the byte budget.
			if err != nil {
				c.close(websocket.CloseAbnormalClosure, "write failed")
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close(websocket.CloseAbnormalClosure, "ping failed")
				return
			}
		}
	}
}
