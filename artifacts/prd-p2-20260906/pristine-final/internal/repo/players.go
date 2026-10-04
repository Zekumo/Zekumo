package repo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Players struct{ DB *pgxpool.Pool }

const playerCols = `id, game_id, provider, identifier, COALESCE(password_hash, ''), nickname, profile, banned, account_id, created_at, last_login_at`

func scanPlayer(row pgx.Row) (*Player, error) {
	var p Player
	err := row.Scan(&p.ID, &p.GameID, &p.Provider, &p.Identifier, &p.PasswordHash,
		&p.Nickname, &p.Profile, &p.Banned, &p.AccountID, &p.CreatedAt, &p.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r Players) Find(ctx context.Context, gameID, provider, identifier string) (*Player, error) {
	return scanPlayer(r.DB.QueryRow(ctx,
		`SELECT `+playerCols+` FROM players WHERE game_id = $1 AND provider = $2 AND identifier = $3`,
		gameID, provider, identifier))
}

func (r Players) ByID(ctx context.Context, id string) (*Player, error) {
	return scanPlayer(r.DB.QueryRow(ctx, `SELECT `+playerCols+` FROM players WHERE id = $1`, id))
}

func (r Players) Create(ctx context.Context, gameID, provider, identifier, passwordHash, nickname string) (*Player, error) {
	return scanPlayer(r.DB.QueryRow(ctx,
		`INSERT INTO players (game_id, provider, identifier, password_hash, nickname)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		 RETURNING `+playerCols,
		gameID, provider, identifier, passwordHash, nickname))
}

func (r Players) SetBanned(ctx context.Context, id string, banned bool) error {
	_, err := r.DB.Exec(ctx, `UPDATE players SET banned = $2 WHERE id = $1`, id, banned)
	return err
}

func (r Players) TouchLogin(ctx context.Context, id string) error {
	_, err := r.DB.Exec(ctx, `UPDATE players SET last_login_at = now() WHERE id = $1`, id)
	return err
}

func (r Players) UpdateProfile(ctx context.Context, id, nickname string, profile json.RawMessage) (*Player, error) {
	return scanPlayer(r.DB.QueryRow(ctx,
		`UPDATE players SET nickname = COALESCE(NULLIF($2, ''), nickname),
		        profile = COALESCE($3, profile)
		 WHERE id = $1 RETURNING `+playerCols,
		id, nickname, profile))
}

func (r Players) ListByGame(ctx context.Context, gameID, search string, limit, offset int) ([]Player, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+playerCols+` FROM players
		 WHERE game_id = $1 AND ($2 = '' OR nickname ILIKE '%'||$2||'%' OR identifier ILIKE '%'||$2||'%')
		 ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
		gameID, search, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	players := []Player{}
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, err
		}
		players = append(players, *p)
	}
	return players, rows.Err()
}

// ByAccount finds the player linked to a platform account in one game.
func (r Players) ByAccount(ctx context.Context, gameID, accountID string) (*Player, error) {
	return scanPlayer(r.DB.QueryRow(ctx,
		`SELECT `+playerCols+` FROM players WHERE game_id = $1 AND account_id = $2`,
		gameID, accountID))
}

// CreateForAccount creates an SSO-born player already linked to the account.
func (r Players) CreateForAccount(ctx context.Context, gameID, accountID, nickname string) (*Player, error) {
	return scanPlayer(r.DB.QueryRow(ctx,
		`INSERT INTO players (game_id, provider, identifier, nickname, account_id)
		 VALUES ($1, 'sso', $2::text, $3, $2::uuid)
		 RETURNING `+playerCols,
		gameID, accountID, nickname))
}

var (
	ErrPlayerAlreadyBound  = errors.New("player is already bound to an account")
	ErrAccountAlreadyBound = errors.New("account already has a player in this game")
)

// IsUniqueViolation reports whether err is a Postgres unique-constraint hit,
// which login paths treat as "someone else created it first — re-fetch".
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Bind links an existing player to a platform account. Fails if the player is
// already bound, or the account already owns a player in the same game.
func (r Players) Bind(ctx context.Context, playerID, accountID string) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE players SET account_id = $2 WHERE id = $1 AND account_id IS NULL`,
		playerID, accountID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAccountAlreadyBound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPlayerAlreadyBound
	}
	return nil
}

// ListByAccount returns every game player linked to an account.
func (r Players) ListByAccount(ctx context.Context, accountID string) ([]LinkedPlayer, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT p.game_id, g.name, p.id, p.nickname
		 FROM players p JOIN games g ON g.id = p.game_id
		 WHERE p.account_id = $1 ORDER BY p.created_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkedPlayer{}
	for rows.Next() {
		var lp LinkedPlayer
		if err := rows.Scan(&lp.GameID, &lp.GameName, &lp.PlayerID, &lp.Nickname); err != nil {
			return nil, err
		}
		out = append(out, lp)
	}
	return out, rows.Err()
}

// Nicknames returns id -> nickname for the given player ids.
func (r Players) Nicknames(ctx context.Context, ids []string) (map[string]string, error) {
	rows, err := r.DB.Query(ctx, `SELECT id, nickname FROM players WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(ids))
	for rows.Next() {
		var id, nick string
		if err := rows.Scan(&id, &nick); err != nil {
			return nil, err
		}
		out[id] = nick
	}
	return out, rows.Err()
}
