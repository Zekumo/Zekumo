package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Announcements manages server-broadcast messages for a game.
type Announcements struct{ DB *pgxpool.Pool }

const annCols = `id, game_id, title, body, importance, platform, active, expires_at, created_at, updated_at`

func scanAnn(row pgx.Row) (*Announcement, error) {
	var a Announcement
	err := row.Scan(&a.ID, &a.GameID, &a.Title, &a.Body, &a.Importance,
		&a.Platform, &a.Active, &a.ExpiresAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

func (r Announcements) Create(ctx context.Context, a Announcement) (*Announcement, error) {
	return scanAnn(r.DB.QueryRow(ctx,
		`INSERT INTO announcements (game_id, title, body, importance, platform, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+annCols,
		a.GameID, a.Title, a.Body, a.Importance, a.Platform, a.ExpiresAt))
}

func (r Announcements) Update(ctx context.Context, id string, a Announcement) (*Announcement, error) {
	return scanAnn(r.DB.QueryRow(ctx,
		`UPDATE announcements
		 SET title=$2, body=$3, importance=$4, platform=$5, expires_at=$6, updated_at=now()
		 WHERE id=$1 RETURNING `+annCols,
		id, a.Title, a.Body, a.Importance, a.Platform, a.ExpiresAt))
}

func (r Announcements) Deactivate(ctx context.Context, id string) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE announcements SET active=FALSE, updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PublicList returns active, non-expired announcements with id > afterID.
// afterID="" returns from the beginning (newest first up to limit).
func (r Announcements) PublicList(ctx context.Context, gameID, afterID string, limit int) ([]Announcement, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.DB.Query(ctx,
		`SELECT `+annCols+`
		 FROM announcements
		 WHERE game_id=$1
		   AND active=TRUE
		   AND (expires_at IS NULL OR expires_at > now())
		   AND ($2='' OR id > $2::uuid)
		 ORDER BY id ASC
		 LIMIT $3`,
		gameID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnns(rows)
}

// AdminList returns all announcements for a game (including inactive), newest first.
func (r Announcements) AdminList(ctx context.Context, gameID string) ([]Announcement, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+annCols+` FROM announcements WHERE game_id=$1 ORDER BY created_at DESC`,
		gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnns(rows)
}

// PublicListByAppID resolves game by app_id then returns active non-expired
// announcements created after the announcement identified by afterID.
func (r Announcements) PublicListByAppID(ctx context.Context, appID, afterID string, limit int) ([]Announcement, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.DB.Query(ctx,
		`SELECT a.id, a.game_id, a.title, a.body, a.importance, a.platform,
		        a.active, a.expires_at, a.created_at, a.updated_at
		 FROM announcements a
		 JOIN games g ON g.id = a.game_id
		 WHERE g.app_id = $1
		   AND a.active = TRUE
		   AND (a.expires_at IS NULL OR a.expires_at > now())
		   AND ($2 = '' OR (a.created_at, a.id) > (
		       SELECT cursor.created_at, cursor.id FROM announcements cursor WHERE cursor.id = $2::uuid
		   ))
		 ORDER BY a.created_at ASC, a.id ASC
		 LIMIT $3`,
		appID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnns(rows)
}

func scanAnns(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Announcement, error) {
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.GameID, &a.Title, &a.Body, &a.Importance,
			&a.Platform, &a.Active, &a.ExpiresAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
