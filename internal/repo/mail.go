package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlreadyClaimed    = errors.New("mail rewards already claimed")
	ErrInvalidRecipients = errors.New("one or more mail recipients are invalid")
	ErrInvalidRewards    = errors.New("one or more mail rewards are invalid")
)

type Mail struct{ DB *pgxpool.Pool }

const mailCols = `id, game_id, broadcast, title, body, rewards, expires_at, created_at`

func scanMail(row pgx.Row) (*MailMessage, error) {
	var m MailMessage
	err := row.Scan(&m.ID, &m.GameID, &m.Broadcast, &m.Title, &m.Body, &m.Rewards, &m.ExpiresAt, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

func (r Mail) Create(ctx context.Context, gameID string, broadcast bool, title, body string, rewards json.RawMessage, expiresAt time.Time) (*MailMessage, error) {
	return scanMail(r.DB.QueryRow(ctx,
		`INSERT INTO mail_messages (game_id, broadcast, title, body, rewards, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+mailCols,
		gameID, broadcast, title, body, rewards, expiresAt))
}

// CreateWithRecipients atomically creates a message and its directed delivery
// rows. It rejects the whole request if any target does not belong to the game,
// preventing an orphan message or a silently partial delivery.
func (r Mail) CreateWithRecipients(ctx context.Context, gameID string, broadcast bool, playerIDs []string, title, body string, rewards json.RawMessage, expiresAt time.Time) (*MailMessage, int, error) {
	var rewardList []MailReward
	if err := json.Unmarshal(rewards, &rewardList); err != nil {
		return nil, 0, ErrInvalidRewards
	}
	seenRewards := make(map[string]struct{}, len(rewardList))
	for _, reward := range rewardList {
		if reward.CurrencyID == "" || reward.Amount <= 0 {
			return nil, 0, ErrInvalidRewards
		}
		if _, duplicate := seenRewards[reward.CurrencyID]; duplicate {
			return nil, 0, ErrInvalidRewards
		}
		seenRewards[reward.CurrencyID] = struct{}{}
	}
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Serialize currency definition changes with mail creation, then take key
	// share locks on every referenced definition. An admin cannot delete a
	// currency between handler validation and committing the reward payload.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, gameID); err != nil {
		return nil, 0, err
	}
	if len(rewardList) > 0 {
		rows, err := tx.Query(ctx,
			`SELECT c.id FROM currencies c
			 JOIN jsonb_array_elements($2::jsonb) reward
			   ON c.id::text = reward->>'currency_id'
			 WHERE c.game_id=$1
			 FOR KEY SHARE OF c`, gameID, rewards)
		if err != nil {
			return nil, 0, err
		}
		matched := 0
		for rows.Next() {
			matched++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, err
		}
		rows.Close()
		if matched != len(rewardList) {
			return nil, 0, ErrInvalidRewards
		}
	}

	var targetCount int
	if broadcast {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM players WHERE game_id=$1`, gameID).Scan(&targetCount); err != nil {
			return nil, 0, err
		}
	} else {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM players WHERE game_id=$1 AND id = ANY($2::uuid[])`,
			gameID, playerIDs).Scan(&targetCount); err != nil {
			return nil, 0, err
		}
		if targetCount != len(playerIDs) {
			return nil, 0, ErrInvalidRecipients
		}
	}

	m, err := scanMail(tx.QueryRow(ctx,
		`INSERT INTO mail_messages (game_id, broadcast, title, body, rewards, expires_at, recipient_count)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+mailCols,
		gameID, broadcast, title, body, rewards, expiresAt, targetCount))
	if err != nil {
		return nil, 0, err
	}
	if !broadcast {
		tag, err := tx.Exec(ctx,
			`INSERT INTO mail_recipients (mail_id, player_id)
			 SELECT $1, p.id FROM players p WHERE p.id = ANY($2::uuid[]) AND p.game_id = $3`,
			m.ID, playerIDs, gameID)
		if err != nil {
			return nil, 0, err
		}
		if int(tag.RowsAffected()) != targetCount {
			return nil, 0, ErrInvalidRecipients
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return m, targetCount, nil
}

func (r Mail) ByID(ctx context.Context, id string) (*MailMessage, error) {
	return scanMail(r.DB.QueryRow(ctx, `SELECT `+mailCols+` FROM mail_messages WHERE id=$1`, id))
}

// AddRecipients inserts delivery rows, silently skipping ids that do not
// belong to the game. Returns how many rows were actually delivered.
func (r Mail) AddRecipients(ctx context.Context, mailID, gameID string, playerIDs []string) (int, error) {
	tag, err := r.DB.Exec(ctx,
		`INSERT INTO mail_recipients (mail_id, player_id)
		 SELECT $1, p.id FROM players p WHERE p.id = ANY($2::uuid[]) AND p.game_id = $3
		 ON CONFLICT DO NOTHING`,
		mailID, playerIDs, gameID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r Mail) Inbox(ctx context.Context, gameID, playerID string, unreadOnly bool, limit int) ([]MailboxItem, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT m.id, m.title, m.body, m.rewards, m.expires_at, m.created_at, r.read_at, r.claimed_at
		 FROM mail_messages m
		 LEFT JOIN mail_recipients r ON r.mail_id = m.id AND r.player_id = $2
		 WHERE m.game_id = $1 AND m.expires_at > now()
		   AND (m.broadcast OR r.player_id IS NOT NULL)
		   AND (NOT $3 OR r.read_at IS NULL)
		 ORDER BY m.created_at DESC LIMIT $4`,
		gameID, playerID, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailboxItem{}
	for rows.Next() {
		var it MailboxItem
		if err := rows.Scan(&it.ID, &it.Title, &it.Body, &it.Rewards, &it.ExpiresAt, &it.CreatedAt, &it.ReadAt, &it.ClaimedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r Mail) UnreadCount(ctx context.Context, gameID, playerID string) (int, error) {
	var n int
	err := r.DB.QueryRow(ctx,
		`SELECT count(*)
		 FROM mail_messages m
		 LEFT JOIN mail_recipients r ON r.mail_id = m.id AND r.player_id = $2
		 WHERE m.game_id = $1 AND m.expires_at > now()
		   AND (m.broadcast OR r.player_id IS NOT NULL)
		   AND r.read_at IS NULL`,
		gameID, playerID).Scan(&n)
	return n, err
}

// MarkRead records read state. For directed mail the caller must be a
// recipient; broadcast mail creates the state row lazily.
func (r Mail) MarkRead(ctx context.Context, m *MailMessage, playerID string) error {
	if m.Broadcast {
		_, err := r.DB.Exec(ctx,
			`INSERT INTO mail_recipients (mail_id, player_id, read_at) VALUES ($1,$2,now())
			 ON CONFLICT (mail_id, player_id)
			   DO UPDATE SET read_at = COALESCE(mail_recipients.read_at, now())`,
			m.ID, playerID)
		return err
	}
	tag, err := r.DB.Exec(ctx,
		`UPDATE mail_recipients SET read_at = COALESCE(read_at, now())
		 WHERE mail_id=$1 AND player_id=$2`,
		m.ID, playerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimTx flips claimed_at exactly once inside the caller's transaction.
func (r Mail) ClaimTx(ctx context.Context, tx pgx.Tx, m *MailMessage, playerID string) (time.Time, error) {
	var claimedAt time.Time
	if m.Broadcast {
		err := tx.QueryRow(ctx,
			`INSERT INTO mail_recipients (mail_id, player_id, read_at, claimed_at) VALUES ($1,$2,now(),now())
			 ON CONFLICT (mail_id, player_id) DO UPDATE
			   SET claimed_at = now(), read_at = COALESCE(mail_recipients.read_at, now())
			   WHERE mail_recipients.claimed_at IS NULL
			 RETURNING claimed_at`,
			m.ID, playerID).Scan(&claimedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return claimedAt, ErrAlreadyClaimed
		}
		return claimedAt, err
	}
	err := tx.QueryRow(ctx,
		`UPDATE mail_recipients SET claimed_at = now(), read_at = COALESCE(read_at, now())
		 WHERE mail_id=$1 AND player_id=$2 AND claimed_at IS NULL
		 RETURNING claimed_at`,
		m.ID, playerID).Scan(&claimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if e := tx.QueryRow(ctx,
			`SELECT true FROM mail_recipients WHERE mail_id=$1 AND player_id=$2`,
			m.ID, playerID).Scan(&exists); e == nil {
			return claimedAt, ErrAlreadyClaimed
		}
		return claimedAt, ErrNotFound
	}
	return claimedAt, err
}

func (r Mail) AdminList(ctx context.Context, gameID string, limit, offset int) ([]MailAdminItem, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+mailCols+`,
		        m.recipient_count,
		        (SELECT count(*) FROM mail_recipients x WHERE x.mail_id = m.id AND x.read_at IS NOT NULL),
		        (SELECT count(*) FROM mail_recipients x WHERE x.mail_id = m.id AND x.claimed_at IS NOT NULL)
		 FROM mail_messages m WHERE game_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		gameID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailAdminItem{}
	for rows.Next() {
		var it MailAdminItem
		if err := rows.Scan(&it.ID, &it.GameID, &it.Broadcast, &it.Title, &it.Body, &it.Rewards,
			&it.ExpiresAt, &it.CreatedAt, &it.Recipients, &it.ReadCount, &it.Claimed); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
