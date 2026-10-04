package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Games struct{ DB *pgxpool.Pool }

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (g Games) Create(ctx context.Context, name string) (*Game, error) {
	game := &Game{
		AppID:     "mc_" + randomHex(6),
		AppSecret: randomHex(24),
		Name:      name,
	}
	err := g.DB.QueryRow(ctx,
		`INSERT INTO games (app_id, app_secret, name) VALUES ($1, $2, $3)
		 RETURNING id, status, created_at, friend_limit`,
		game.AppID, game.AppSecret, game.Name,
	).Scan(&game.ID, &game.Status, &game.CreatedAt, &game.FriendLimit)
	if err != nil {
		return nil, err
	}
	return game, nil
}

const gameCols = `id, app_id, app_secret, name, status, created_at, sso_redirect_urls, func_http_allowlist, friend_limit`

func (g Games) ByAppID(ctx context.Context, appID string) (*Game, error) {
	return g.scanOne(g.DB.QueryRow(ctx,
		`SELECT `+gameCols+` FROM games WHERE app_id = $1`, appID))
}

func (g Games) ByID(ctx context.Context, id string) (*Game, error) {
	return g.scanOne(g.DB.QueryRow(ctx,
		`SELECT `+gameCols+` FROM games WHERE id = $1`, id))
}

func (g Games) scanOne(row pgx.Row) (*Game, error) {
	var game Game
	err := row.Scan(&game.ID, &game.AppID, &game.AppSecret, &game.Name, &game.Status, &game.CreatedAt,
		&game.SSORedirectURLs, &game.FuncHTTPAllowlist, &game.FriendLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &game, nil
}

func (g Games) UpdateFriendLimit(ctx context.Context, id string, limit int) error {
	tag, err := g.DB.Exec(ctx, `UPDATE games SET friend_limit = $2 WHERE id = $1`, id, limit)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (g Games) List(ctx context.Context) ([]Game, error) {
	rows, err := g.DB.Query(ctx,
		`SELECT `+gameCols+` FROM games ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	games := []Game{}
	for rows.Next() {
		game, err := g.scanOne(rows)
		if err != nil {
			return nil, err
		}
		games = append(games, *game)
	}
	return games, rows.Err()
}

// UpdateFuncHTTPAllowlist stores the newline-separated outbound-host whitelist.
func (g Games) UpdateFuncHTTPAllowlist(ctx context.Context, id, hosts string) error {
	tag, err := g.DB.Exec(ctx, `UPDATE games SET func_http_allowlist = $2 WHERE id = $1`, id, hosts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateSSORedirectURLs stores the newline-separated redirect whitelist.
func (g Games) UpdateSSORedirectURLs(ctx context.Context, id, urls string) error {
	tag, err := g.DB.Exec(ctx, `UPDATE games SET sso_redirect_urls = $2 WHERE id = $1`, id, urls)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (g Games) Delete(ctx context.Context, id string) error {
	tag, err := g.DB.Exec(ctx, `DELETE FROM games WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
