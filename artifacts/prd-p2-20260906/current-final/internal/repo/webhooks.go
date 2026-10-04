package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Webhook struct {
	ID        string    `json:"id"`
	GameID    string    `json:"game_id"`
	URL       string    `json:"url"`
	Secret    string    `json:"secret,omitempty"`
	Events    string    `json:"events"` // comma-separated event names, or *
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type WebhookDelivery struct {
	ID        int64           `json:"id"`
	WebhookID string          `json:"webhook_id"`
	Event     string          `json:"event"`
	Payload   json.RawMessage `json:"payload"`
	Status    int             `json:"status"` // last HTTP status, 0 = network error
	Attempts  int             `json:"attempts"`
	OK        bool            `json:"ok"`
	CreatedAt time.Time       `json:"created_at"`
}

type Webhooks struct{ DB *pgxpool.Pool }

const webhookCols = `id, game_id, url, secret, events, enabled, created_at`

func scanWebhook(row pgx.Row) (*Webhook, error) {
	var h Webhook
	err := row.Scan(&h.ID, &h.GameID, &h.URL, &h.Secret, &h.Events, &h.Enabled, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func (r Webhooks) Create(ctx context.Context, gameID, url, events string) (*Webhook, error) {
	return scanWebhook(r.DB.QueryRow(ctx,
		`INSERT INTO webhooks (game_id, url, secret, events) VALUES ($1, $2, $3, $4)
		 RETURNING `+webhookCols, gameID, url, "whs_"+randomHex(24), events))
}

func (r Webhooks) ByID(ctx context.Context, id string) (*Webhook, error) {
	return scanWebhook(r.DB.QueryRow(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id = $1`, id))
}

func (r Webhooks) ListByGame(ctx context.Context, gameID string) ([]Webhook, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+webhookCols+` FROM webhooks WHERE game_id = $1 ORDER BY created_at`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hooks := []Webhook{}
	for rows.Next() {
		h, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, *h)
	}
	return hooks, rows.Err()
}

// ListEnabled returns enabled hooks for a game (event filtering happens in the bus).
func (r Webhooks) ListEnabled(ctx context.Context, gameID string) ([]Webhook, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+webhookCols+` FROM webhooks WHERE game_id = $1 AND enabled`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hooks := []Webhook{}
	for rows.Next() {
		h, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, *h)
	}
	return hooks, rows.Err()
}

func (r Webhooks) Update(ctx context.Context, id, url, events string, enabled bool) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE webhooks SET url = $2, events = $3, enabled = $4 WHERE id = $1`,
		id, url, events, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r Webhooks) Delete(ctx context.Context, id string) error {
	tag, err := r.DB.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type WebhookDeliveries struct{ DB *pgxpool.Pool }

func (r WebhookDeliveries) Record(ctx context.Context, d *WebhookDelivery) error {
	return r.DB.QueryRow(ctx,
		`INSERT INTO webhook_deliveries (webhook_id, event, payload, status, attempts, ok)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		d.WebhookID, d.Event, d.Payload, d.Status, d.Attempts, d.OK,
	).Scan(&d.ID, &d.CreatedAt)
}

func (r WebhookDeliveries) Recent(ctx context.Context, webhookID string, limit int) ([]WebhookDelivery, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT id, webhook_id, event, payload, status, attempts, ok, created_at
		 FROM webhook_deliveries WHERE webhook_id = $1 ORDER BY id DESC LIMIT $2`,
		webhookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebhookDelivery{}
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Payload, &d.Status, &d.Attempts, &d.OK, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
