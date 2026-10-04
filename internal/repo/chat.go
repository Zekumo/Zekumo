package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Chat struct{ DB *pgxpool.Pool }

func (r Chat) Save(ctx context.Context, m *ChatMessage) error {
	return r.DB.QueryRow(ctx,
		`INSERT INTO chat_messages (game_id, channel, sender_id, sender_name, content)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		m.GameID, m.Channel, m.SenderID, m.SenderName, m.Content,
	).Scan(&m.ID, &m.CreatedAt)
}

// History returns messages newest-first, optionally only those older than beforeID.
func (r Chat) History(ctx context.Context, gameID, channel string, beforeID int64, limit int) ([]ChatMessage, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT id, game_id, channel, sender_id, sender_name, content, created_at
		 FROM chat_messages
		 WHERE game_id = $1 AND channel = $2 AND ($3 = 0 OR id < $3)
		 ORDER BY id DESC LIMIT $4`,
		gameID, channel, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.GameID, &m.Channel, &m.SenderID, &m.SenderName, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
