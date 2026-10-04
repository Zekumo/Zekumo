package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrIdemConflict      = errors.New("idempotency key reused with different parameters")
	ErrCurrencyLimit     = errors.New("currency limit reached")
	ErrCurrencyInUse     = errors.New("currency has balances, ledger entries or pending rewards")
)

type Currencies struct{ DB *pgxpool.Pool }

const currencyCols = `id, game_id, name, display_name, icon_url, created_at`

func scanCurrency(row pgx.Row) (*Currency, error) {
	var c Currency
	err := row.Scan(&c.ID, &c.GameID, &c.Name, &c.DisplayName, &c.IconURL, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

func (r Currencies) Create(ctx context.Context, gameID string, c Currency) (*Currency, error) {
	return scanCurrency(r.DB.QueryRow(ctx,
		`INSERT INTO currencies (game_id, name, display_name, icon_url)
		 VALUES ($1,$2,$3,$4) RETURNING `+currencyCols,
		gameID, c.Name, c.DisplayName, c.IconURL))
}

// CreateLimited serializes definition creation per game and enforces the
// configured maximum inside the same transaction. A handler-side count alone
// would allow concurrent requests to exceed the limit.
func (r Currencies) CreateLimited(ctx context.Context, gameID string, c Currency, max int) (*Currency, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, gameID); err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM currencies WHERE game_id=$1`, gameID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= max {
		return nil, ErrCurrencyLimit
	}
	cur, err := scanCurrency(tx.QueryRow(ctx,
		`INSERT INTO currencies (game_id, name, display_name, icon_url)
		 VALUES ($1,$2,$3,$4) RETURNING `+currencyCols,
		gameID, c.Name, c.DisplayName, c.IconURL))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return cur, nil
}

func (r Currencies) Update(ctx context.Context, id string, c Currency) (*Currency, error) {
	return scanCurrency(r.DB.QueryRow(ctx,
		`UPDATE currencies SET display_name=$2, icon_url=$3 WHERE id=$1 RETURNING `+currencyCols,
		id, c.DisplayName, c.IconURL))
}

func (r Currencies) Delete(ctx context.Context, id string) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var gameID string
	if err := tx.QueryRow(ctx, `SELECT game_id FROM currencies WHERE id=$1`, id).Scan(&gameID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, gameID); err != nil {
		return err
	}
	var inUse bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM currency_balances WHERE currency_id=$1::uuid)
		     OR EXISTS (SELECT 1 FROM currency_ledger WHERE currency_id=$1::uuid)
		     OR EXISTS (
		       SELECT 1 FROM mail_messages m, jsonb_array_elements(m.rewards) reward
		       WHERE reward->>'currency_id'=$1 AND m.expires_at > now()
		     )`, id).Scan(&inUse)
	if err != nil {
		return err
	}
	if inUse {
		return ErrCurrencyInUse
	}
	tag, err := tx.Exec(ctx, `DELETE FROM currencies WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (r Currencies) ByID(ctx context.Context, id string) (*Currency, error) {
	return scanCurrency(r.DB.QueryRow(ctx,
		`SELECT `+currencyCols+` FROM currencies WHERE id=$1`, id))
}

func (r Currencies) ByName(ctx context.Context, gameID, name string) (*Currency, error) {
	return scanCurrency(r.DB.QueryRow(ctx,
		`SELECT `+currencyCols+` FROM currencies WHERE game_id=$1 AND name=$2`, gameID, name))
}

func (r Currencies) ByGame(ctx context.Context, gameID string) ([]Currency, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+currencyCols+` FROM currencies WHERE game_id=$1 ORDER BY created_at`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Currency{}
	for rows.Next() {
		c, err := scanCurrency(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r Currencies) CountByGame(ctx context.Context, gameID string) (int, error) {
	var n int
	err := r.DB.QueryRow(ctx, `SELECT count(*) FROM currencies WHERE game_id=$1`, gameID).Scan(&n)
	return n, err
}

type Wallets struct{ DB *pgxpool.Pool }

// ApplyTx moves amount (positive = credit, negative = debit) inside the given
// transaction and appends the ledger row. A unique violation on the ledger
// bubbles up so the caller can resolve idempotent replays.
func (r Wallets) ApplyTx(ctx context.Context, tx pgx.Tx, currencyID, playerID string, amount int64, kind, idemKey, note string) (int64, error) {
	var balance int64
	if amount >= 0 {
		err := tx.QueryRow(ctx,
			`INSERT INTO currency_balances (currency_id, player_id, balance)
			 VALUES ($1,$2,$3)
			 ON CONFLICT (currency_id, player_id)
			   DO UPDATE SET balance = currency_balances.balance + EXCLUDED.balance, updated_at = now()
			 RETURNING balance`,
			currencyID, playerID, amount).Scan(&balance)
		if err != nil {
			return 0, err
		}
	} else {
		err := tx.QueryRow(ctx,
			`UPDATE currency_balances SET balance = balance + $3::bigint, updated_at = now()
			 WHERE currency_id=$1 AND player_id=$2 AND balance >= $4::bigint
			 RETURNING balance`,
			currencyID, playerID, amount, -amount).Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrInsufficientFunds
		}
		if err != nil {
			return 0, err
		}
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO currency_ledger (currency_id, player_id, amount, balance_after, kind, idempotency_key, note)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		currencyID, playerID, amount, balance, kind, idemKey, note)
	return balance, err
}

// Apply runs ApplyTx in its own transaction. Replaying the same idempotency
// key returns the original outcome with duplicate=true; reusing the key with
// a different amount returns ErrIdemConflict.
func (r Wallets) Apply(ctx context.Context, currencyID, playerID string, amount int64, kind, idemKey, note string) (balance int64, duplicate bool, err error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	balance, err = r.ApplyTx(ctx, tx, currencyID, playerID, amount, kind, idemKey, note)
	if err == nil {
		return balance, false, tx.Commit(ctx)
	}
	_ = tx.Rollback(ctx)
	if !IsUniqueViolation(err) {
		return 0, false, err
	}
	prev, lookupErr := r.LedgerEntry(ctx, currencyID, playerID, idemKey)
	if lookupErr != nil {
		return 0, false, lookupErr
	}
	if prev.Amount != amount || prev.Kind != kind || prev.Note != note {
		return 0, false, ErrIdemConflict
	}
	return prev.BalanceAfter, true, nil
}

func (r Wallets) LedgerEntry(ctx context.Context, currencyID, playerID, idemKey string) (*LedgerEntry, error) {
	var e LedgerEntry
	err := r.DB.QueryRow(ctx,
		`SELECT id, currency_id, player_id, amount, balance_after, kind, idempotency_key, note, created_at
		 FROM currency_ledger WHERE currency_id=$1 AND player_id=$2 AND idempotency_key=$3`,
		currencyID, playerID, idemKey,
	).Scan(&e.ID, &e.CurrencyID, &e.PlayerID, &e.Amount, &e.BalanceAfter, &e.Kind, &e.IdempotencyKey, &e.Note, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &e, err
}

func (r Wallets) Balances(ctx context.Context, gameID, playerID string) ([]CurrencyBalance, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT c.id, c.name, c.display_name, c.icon_url, COALESCE(b.balance, 0)
		 FROM currencies c
		 LEFT JOIN currency_balances b ON b.currency_id = c.id AND b.player_id = $2
		 WHERE c.game_id = $1 ORDER BY c.created_at`,
		gameID, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CurrencyBalance{}
	for rows.Next() {
		var b CurrencyBalance
		if err := rows.Scan(&b.CurrencyID, &b.Name, &b.DisplayName, &b.IconURL, &b.Balance); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r Wallets) Ledger(ctx context.Context, currencyID, playerID string, limit, offset int) ([]LedgerEntry, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT id, currency_id, player_id, amount, balance_after, kind, idempotency_key, note, created_at
		 FROM currency_ledger WHERE currency_id=$1 AND player_id=$2
		 ORDER BY id DESC LIMIT $3 OFFSET $4`,
		currencyID, playerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.CurrencyID, &e.PlayerID, &e.Amount, &e.BalanceAfter, &e.Kind, &e.IdempotencyKey, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
