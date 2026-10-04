CREATE TABLE IF NOT EXISTS currencies (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id      UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    icon_url     TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, name)
);

CREATE TABLE IF NOT EXISTS currency_balances (
    currency_id UUID NOT NULL REFERENCES currencies(id) ON DELETE CASCADE,
    player_id   UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    balance     BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (currency_id, player_id)
);
CREATE INDEX IF NOT EXISTS idx_currency_balances_player ON currency_balances (player_id);

CREATE TABLE IF NOT EXISTS currency_ledger (
    id              BIGSERIAL PRIMARY KEY,
    currency_id     UUID NOT NULL REFERENCES currencies(id) ON DELETE CASCADE,
    player_id       UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    amount          BIGINT NOT NULL,
    balance_after   BIGINT NOT NULL,
    kind            TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    note            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (currency_id, player_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_currency_ledger_player ON currency_ledger (currency_id, player_id, id DESC);
