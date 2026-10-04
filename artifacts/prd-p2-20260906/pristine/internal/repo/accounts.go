package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Accounts are platform-level identities (通行证): one account can log into
// every game on the platform; per-game players link to it via account_id.
type Accounts struct{ DB *pgxpool.Pool }

const accountCols = `id, username, password_hash, nickname, created_at, last_login_at`

func scanAccount(row pgx.Row) (*Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Nickname, &a.CreatedAt, &a.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r Accounts) Create(ctx context.Context, username, passwordHash, nickname string) (*Account, error) {
	return scanAccount(r.DB.QueryRow(ctx,
		`INSERT INTO accounts (username, password_hash, nickname) VALUES ($1, $2, $3)
		 RETURNING `+accountCols, username, passwordHash, nickname))
}

func (r Accounts) ByUsername(ctx context.Context, username string) (*Account, error) {
	return scanAccount(r.DB.QueryRow(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE username = $1`, username))
}

func (r Accounts) ByID(ctx context.Context, id string) (*Account, error) {
	return scanAccount(r.DB.QueryRow(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE id = $1`, id))
}

func (r Accounts) TouchLogin(ctx context.Context, id string) error {
	_, err := r.DB.Exec(ctx, `UPDATE accounts SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// Delete removes a platform account. Players keep their save data and are
// unbound (players.account_id is ON DELETE SET NULL); grants and refresh
// tokens cascade away with it.
func (r Accounts) Delete(ctx context.Context, id string) error {
	tag, err := r.DB.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r Accounts) List(ctx context.Context, search string, limit, offset int) ([]Account, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+accountCols+` FROM accounts
		 WHERE $1 = '' OR username ILIKE '%'||$1||'%' OR nickname ILIKE '%'||$1||'%'
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, *a)
	}
	return accounts, rows.Err()
}
