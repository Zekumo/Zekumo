// Package hooks delivers platform events (player.registered, player.login,
// leaderboard.score, release.published, ...) to developer-configured webhook
// URLs, signed with HMAC-SHA256 and retried on failure.
package hooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"zekumo/internal/netsafe"
	"zekumo/internal/repo"
	"zekumo/internal/safego"
)

const (
	queueSize    = 1024
	workerCount  = 8
	maxAttempts  = 3
	requestLimit = 5 * time.Second
)

var retryDelays = []time.Duration{0, time.Second, 5 * time.Second}

type job struct {
	gameID  string
	event   string
	payload []byte
}

// LogSink receives structured log entries; nil disables logging.
type LogSink interface {
	Write(gameID, level, source, event, message string, fields map[string]any)
}

type Bus struct {
	Webhooks   repo.Webhooks
	Deliveries repo.WebhookDeliveries
	Logs       LogSink

	queue  chan job
	client *http.Client
}

// NewBus starts the delivery workers. allowPrivate lets webhooks reach
// private addresses; keep it false outside local development, or the
// delivery path becomes an SSRF probe into the platform's own network.
func NewBus(webhooks repo.Webhooks, deliveries repo.WebhookDeliveries, allowPrivate bool) *Bus {
	b := &Bus{
		Webhooks:   webhooks,
		Deliveries: deliveries,
		queue:      make(chan job, queueSize),
		client:     netsafe.Client(requestLimit, allowPrivate, nil),
	}
	for i := range workerCount {
		safego.Go(fmt.Sprintf("hooks.worker[%d]", i), b.worker)
	}
	return b
}

// Emit queues an event; it never blocks the caller. Payload must be
// JSON-serializable. Drops the event if the queue is saturated.
func (b *Bus) Emit(gameID, event string, data any) {
	body, err := json.Marshal(map[string]any{
		"event":     event,
		"game_id":   gameID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      data,
	})
	if err != nil {
		log.Printf("hooks: marshal %s failed: %v", event, err)
		return
	}
	select {
	case b.queue <- job{gameID: gameID, event: event, payload: body}:
	default:
		log.Printf("hooks: queue full, dropping %s for game %s", event, gameID)
	}
}

func eventMatches(configured, event string) bool {
	for _, e := range strings.Split(configured, ",") {
		e = strings.TrimSpace(e)
		if e == "*" || e == event {
			return true
		}
	}
	return false
}

func (b *Bus) worker() {
	for j := range b.queue {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		hooks, err := b.Webhooks.ListEnabled(ctx, j.gameID)
		cancel()
		if err != nil {
			log.Printf("hooks: list for game %s failed: %v", j.gameID, err)
			continue
		}
		var wg sync.WaitGroup
		for _, hook := range hooks {
			if !eventMatches(hook.Events, j.event) {
				continue
			}
			wg.Add(1)
			go func() { // one slow endpoint must not delay a game's other hooks
				defer wg.Done()
				safego.Run("hooks.deliver", func() { b.deliver(hook, j) })
			}()
		}
		wg.Wait()
	}
}

// deliver POSTs with retries and records the outcome.
func (b *Bus) deliver(hook repo.Webhook, j job) {
	status, attempts := 0, 0
	for attempt := range maxAttempts {
		time.Sleep(retryDelays[attempt])
		attempts++
		status = b.post(hook, j)
		if status >= 200 && status < 300 {
			break
		}
	}
	d := &repo.WebhookDelivery{
		WebhookID: hook.ID,
		Event:     j.event,
		Payload:   j.payload,
		Status:    status,
		Attempts:  attempts,
		OK:        status >= 200 && status < 300,
	}
	if !d.OK && b.Logs != nil {
		b.Logs.Write(j.gameID, "error", "hooks", j.event,
			"webhook delivery failed: "+hook.URL,
			map[string]any{"status": status, "attempts": attempts, "webhook_id": hook.ID})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Deliveries.Record(ctx, d); err != nil {
		log.Printf("hooks: record delivery failed: %v", err)
	}
}

func (b *Bus) post(hook repo.Webhook, j job) int {
	req, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(j.payload))
	if err != nil {
		return 0
	}
	mac := hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(j.payload)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Zekumo-Event", j.event)
	req.Header.Set("X-Zekumo-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	res, err := b.client.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	return res.StatusCode
}
