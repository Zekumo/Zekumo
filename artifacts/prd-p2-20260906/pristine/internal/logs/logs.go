// Package logs is the structured logging pipeline: every subsystem (HTTP
// access, cloud functions, webhooks, game clients) writes typed entries that
// are batched into Postgres and queryable from the console. Retention cleanup
// runs on a real schedule.
package logs

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"minicloud/internal/safego"
)

const (
	queueSize     = 4096
	batchSize     = 100
	flushInterval = time.Second
	maxMessageLen = 2000
	maxEventLen   = 200
	maxFieldsLen  = 8 << 10
)

type Entry struct {
	ID        int64           `json:"id"`
	GameID    string          `json:"game_id,omitempty"`
	Level     string          `json:"level"` // debug | info | warn | error
	Source    string          `json:"source"`
	Event     string          `json:"event,omitempty"`
	Message   string          `json:"message"`
	Fields    json.RawMessage `json:"fields,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type Service struct {
	DB            *pgxpool.Pool
	RetentionDays int

	queue chan Entry
	done  chan struct{} // closed once the writer has flushed and exited
}

func NewService(ctx context.Context, db *pgxpool.Pool, retentionDays int) *Service {
	s := &Service{
		DB: db, RetentionDays: retentionDays,
		queue: make(chan Entry, queueSize),
		done:  make(chan struct{}),
	}
	safego.Go("logs.writer", func() {
		defer close(s.done)
		s.writer(ctx)
	})
	return s
}

// Wait blocks until the writer has flushed what it had buffered, so a caller
// shutting down can close the database only after the last batch landed.
func (s *Service) Wait(ctx context.Context) {
	select {
	case <-s.done:
	case <-ctx.Done():
	}
}

// truncate cuts s to at most n bytes without splitting a rune. Slicing bytes
// blindly can leave a partial rune, and Postgres rejects invalid UTF-8 —
// which would fail the write for the whole batch.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Write queues an entry; never blocks callers (drops under extreme pressure).
func (s *Service) Write(gameID, level, source, event, message string, fields map[string]any) {
	message = truncate(message, maxMessageLen)
	event = truncate(event, maxEventLen)
	raw := json.RawMessage("{}")
	if fields != nil {
		if b, err := json.Marshal(fields); err == nil && len(b) <= maxFieldsLen {
			raw = b
		}
	}
	e := Entry{GameID: gameID, Level: level, Source: source, Event: event, Message: message,
		Fields: raw, CreatedAt: time.Now().UTC()}
	select {
	case s.queue <- e:
	default:
	}
}

func (s *Service) writer(ctx context.Context) {
	batch := make([]Entry, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case e := <-s.queue:
			batch = append(batch, e)
			if len(batch) >= batchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-ctx.Done():
			// Drain what is already queued so a shutdown does not throw away
			// the last second of logs.
			for {
				select {
				case e := <-s.queue:
					batch = append(batch, e)
					continue
				default:
				}
				break
			}
			if len(batch) > 0 {
				s.flush(batch)
			}
			return
		}
	}
}

// flush writes the batch in a single round trip. One INSERT per entry would
// make the writer goroutine the throughput ceiling for the whole API.
func (s *Service) flush(batch []Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rows := make([][]any, len(batch))
	for i, e := range batch {
		var gameID any
		if e.GameID != "" {
			gameID = e.GameID
		}
		rows[i] = []any{gameID, e.Level, e.Source, e.Event, e.Message, e.Fields, e.CreatedAt}
	}
	_, err := s.DB.CopyFrom(ctx, pgx.Identifier{"app_logs"},
		[]string{"game_id", "level", "source", "event", "message", "fields", "created_at"},
		pgx.CopyFromRows(rows))
	if err != nil {
		log.Printf("logs: flush of %d entries failed: %v", len(batch), err)
	}
}

// StartRetention deletes entries older than RetentionDays — runs once at
// startup and then daily (scheduled for real, not just defined).
func (s *Service) StartRetention(ctx context.Context) {
	run := func() {
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// Webhook delivery records store a full payload per attempt and have
		// no cleanup of their own; age them out on the same schedule.
		if _, err := s.DB.Exec(cctx,
			`DELETE FROM webhook_deliveries WHERE created_at < now() - make_interval(days => $1)`,
			s.RetentionDays); err != nil {
			log.Printf("logs: webhook delivery cleanup failed: %v", err)
		}
		tag, err := s.DB.Exec(cctx,
			`DELETE FROM app_logs WHERE created_at < now() - make_interval(days => $1)`,
			s.RetentionDays)
		if err != nil {
			log.Printf("logs: retention cleanup failed: %v", err)
			return
		}
		if n := tag.RowsAffected(); n > 0 {
			log.Printf("logs: retention removed %d entries older than %d days", n, s.RetentionDays)
		}
	}
	safego.Go("logs.retention", func() {
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	})
}

type QueryFilter struct {
	GameID   string
	Level    string
	Source   string
	Search   string
	BeforeID int64
	Limit    int
}

// escapeLike neutralizes wildcards so a search for "%" means a literal
// percent sign rather than a full-table pattern scan.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(s)
}

// Query returns entries newest-first with optional filters.
func (s *Service) Query(ctx context.Context, f QueryFilter) ([]Entry, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, COALESCE(game_id::text, ''), level, source, event, message, fields, created_at
		 FROM app_logs
		 WHERE ($1 = '' OR game_id::text = $1)
		   AND ($2 = '' OR level = $2)
		   AND ($3 = '' OR source = $3)
		   AND ($4 = '' OR message ILIKE '%'||$4||'%' ESCAPE '\' OR event ILIKE '%'||$4||'%' ESCAPE '\')
		   AND ($5 = 0 OR id < $5)
		 ORDER BY id DESC LIMIT $6`,
		f.GameID, f.Level, f.Source, escapeLike(f.Search), f.BeforeID, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.GameID, &e.Level, &e.Source, &e.Event, &e.Message, &e.Fields, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
