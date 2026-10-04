package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OAuthClient struct {
	ID           string    `json:"id"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	Name         string    `json:"name"`
	RedirectURLs string    `json:"redirect_urls"`
	CreatedAt    time.Time `json:"created_at"`
}

type OAuthGrant struct {
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	Scope      string    `json:"scope"`
	CreatedAt  time.Time `json:"created_at"`
}

type OAuthClients struct{ DB *pgxpool.Pool }

const oauthClientCols = `id, client_id, client_secret, name, redirect_urls, created_at`

func scanOAuthClient(row pgx.Row) (*OAuthClient, error) {
	var c OAuthClient
	err := row.Scan(&c.ID, &c.ClientID, &c.ClientSecret, &c.Name, &c.RedirectURLs, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Create registers a client. confidential=false makes a public (PKCE-only)
// client with no secret, e.g. a mobile app or SPA.
func (r OAuthClients) Create(ctx context.Context, name, redirectURLs string, confidential bool) (*OAuthClient, error) {
	secret := ""
	if confidential {
		secret = randomHex(24)
	}
	return scanOAuthClient(r.DB.QueryRow(ctx,
		`INSERT INTO oauth_clients (client_id, client_secret, name, redirect_urls)
		 VALUES ($1, $2, $3, $4) RETURNING `+oauthClientCols,
		"oc_"+randomHex(8), secret, name, redirectURLs))
}

func (r OAuthClients) ByClientID(ctx context.Context, clientID string) (*OAuthClient, error) {
	return scanOAuthClient(r.DB.QueryRow(ctx,
		`SELECT `+oauthClientCols+` FROM oauth_clients WHERE client_id = $1`, clientID))
}

func (r OAuthClients) List(ctx context.Context) ([]OAuthClient, error) {
	rows, err := r.DB.Query(ctx, `SELECT `+oauthClientCols+` FROM oauth_clients ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clients := []OAuthClient{}
	for rows.Next() {
		c, err := scanOAuthClient(rows)
		if err != nil {
			return nil, err
		}
		clients = append(clients, *c)
	}
	return clients, rows.Err()
}

func (r OAuthClients) Update(ctx context.Context, clientID, name, redirectURLs string) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE oauth_clients SET name = COALESCE(NULLIF($2, ''), name), redirect_urls = $3
		 WHERE client_id = $1`, clientID, name, redirectURLs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r OAuthClients) Delete(ctx context.Context, clientID string) error {
	tag, err := r.DB.Exec(ctx, `DELETE FROM oauth_clients WHERE client_id = $1`, clientID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type OAuthGrants struct{ DB *pgxpool.Pool }

func (r OAuthGrants) Upsert(ctx context.Context, accountID, clientPK, scope string) error {
	_, err := r.DB.Exec(ctx,
		`INSERT INTO oauth_grants (account_id, client_pk, scope) VALUES ($1, $2, $3)
		 ON CONFLICT (account_id, client_pk) DO UPDATE SET scope = EXCLUDED.scope`,
		accountID, clientPK, scope)
	return err
}

// ListByAccount returns what the account has authorized, for the manage page.
func (r OAuthGrants) ListByAccount(ctx context.Context, accountID string) ([]OAuthGrant, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT c.client_id, c.name, g.scope, g.created_at
		 FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_pk
		 WHERE g.account_id = $1 ORDER BY g.created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []OAuthGrant{}
	for rows.Next() {
		var g OAuthGrant
		if err := rows.Scan(&g.ClientID, &g.ClientName, &g.Scope, &g.CreatedAt); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// Exists reports whether the account still grants this client, keyed by the
// public client_id so callers can check straight from an access token.
func (r OAuthGrants) Exists(ctx context.Context, accountID, clientID string) (bool, error) {
	var ok bool
	err := r.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_pk
		                WHERE g.account_id = $1 AND c.client_id = $2)`,
		accountID, clientID).Scan(&ok)
	return ok, err
}

// Revoke removes the grant and every refresh token issued under it.
func (r OAuthGrants) Revoke(ctx context.Context, accountID, clientPK string) error {
	tag, err := r.DB.Exec(ctx,
		`DELETE FROM oauth_grants WHERE account_id = $1 AND client_pk = $2`, accountID, clientPK)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = r.DB.Exec(ctx,
		`DELETE FROM oauth_refresh_tokens WHERE account_id = $1 AND client_pk = $2`, accountID, clientPK)
	return err
}

type RefreshTokenRow struct {
	ID        int64
	Family    string
	AccountID string
	ClientPK  string
	Scope     string
	Used      bool
	ExpiresAt time.Time
}

type OAuthRefreshTokens struct{ DB *pgxpool.Pool }

func (r OAuthRefreshTokens) Insert(ctx context.Context, tokenHash, family, accountID, clientPK, scope string, expiresAt time.Time) error {
	_, err := r.DB.Exec(ctx,
		`INSERT INTO oauth_refresh_tokens (token_hash, family, account_id, client_pk, scope, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		tokenHash, family, accountID, clientPK, scope, expiresAt)
	return err
}

func (r OAuthRefreshTokens) ByHash(ctx context.Context, tokenHash string) (*RefreshTokenRow, error) {
	var t RefreshTokenRow
	err := r.DB.QueryRow(ctx,
		`SELECT id, family, account_id, client_pk, scope, used, expires_at
		 FROM oauth_refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.Family, &t.AccountID, &t.ClientPK, &t.Scope, &t.Used, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// MarkUsed flips the token to used exactly once; returns false if it already was.
func (r OAuthRefreshTokens) MarkUsed(ctx context.Context, id int64) (bool, error) {
	tag, err := r.DB.Exec(ctx,
		`UPDATE oauth_refresh_tokens SET used = TRUE WHERE id = $1 AND NOT used`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RevokeFamily kills a whole rotation chain (used on refresh-token reuse).
func (r OAuthRefreshTokens) RevokeFamily(ctx context.Context, family string) error {
	_, err := r.DB.Exec(ctx, `DELETE FROM oauth_refresh_tokens WHERE family = $1`, family)
	return err
}
