package repo

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAccountSharedAcrossWorkspaces = errors.New("account is linked to another workspace")

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

// ListByWorkspace returns global passport accounts only when they are linked
// to a player in a game owned by the selected workspace.
func (r Accounts) ListByWorkspace(ctx context.Context, workspaceID, search string, limit, offset int) ([]Account, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT DISTINCT `+stringsWithAlias("a", accountCols)+`
		 FROM accounts a JOIN players p ON p.account_id=a.id JOIN games g ON g.id=p.game_id
		 WHERE g.workspace_id=$1
		   AND ($2='' OR a.username ILIKE '%'||$2||'%' OR a.nickname ILIKE '%'||$2||'%')
		 ORDER BY a.created_at DESC LIMIT $3 OFFSET $4`, workspaceID, search, limit, offset)
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

// stringsWithAlias is intentionally tiny and only used with compile-time
// column lists in this package.
func stringsWithAlias(alias, cols string) string {
	parts := strings.Split(cols, ", ")
	for i := range parts {
		parts[i] = alias + "." + parts[i]
	}
	return strings.Join(parts, ", ")
}

// DeleteForWorkspace refuses to delete a passport that is linked to any game
// outside the selected workspace. This prevents one tenant from breaking a
// user's identity in another tenant.
func (r Accounts) DeleteForWorkspace(ctx context.Context, workspaceID, id string) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var here, elsewhere bool
	err = tx.QueryRow(ctx,
		`SELECT
		 EXISTS (SELECT 1 FROM players p JOIN games g ON g.id=p.game_id WHERE p.account_id=$1 AND g.workspace_id=$2),
		 EXISTS (SELECT 1 FROM players p JOIN games g ON g.id=p.game_id WHERE p.account_id=$1 AND g.workspace_id<>$2)`,
		id, workspaceID).Scan(&here, &elsewhere)
	if err != nil {
		return err
	}
	if !here {
		return ErrNotFound
	}
	if elsewhere {
		return ErrAccountSharedAcrossWorkspaces
	}
	tag, err := tx.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
