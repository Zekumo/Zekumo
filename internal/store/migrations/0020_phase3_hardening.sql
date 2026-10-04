-- Preserve applied migrations: Phase 3 correctness additions are forward-only.

-- Snapshot the intended audience when mail is created so broadcast delivery
-- and claim rates have a stable denominator. Backfill existing mail with the
-- best available audience count at migration time.
ALTER TABLE mail_messages
    ADD COLUMN IF NOT EXISTS recipient_count INT NOT NULL DEFAULT 0
    CHECK (recipient_count >= 0);

UPDATE mail_messages m
SET recipient_count = CASE
    WHEN m.broadcast THEN (SELECT count(*) FROM players p WHERE p.game_id = m.game_id)
    ELSE (SELECT count(*) FROM mail_recipients r WHERE r.mail_id = m.id)
END
WHERE m.recipient_count = 0;

-- players.last_login_at cannot answer exact day-N retention after a later
-- login overwrites it. Keep one durable row per player and local login day.
CREATE TABLE IF NOT EXISTS player_login_daily (
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id  UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    login_date DATE NOT NULL,
    PRIMARY KEY (game_id, player_id, login_date)
);

CREATE INDEX IF NOT EXISTS idx_player_login_daily_cohort
    ON player_login_daily (game_id, login_date, player_id);

-- Earlier values were all derived from last_login_at and therefore made D1,
-- D7 and D30 equivalent. Leave historical cohorts pending; the worker only
-- finalizes cohorts that have same-day identity rows in the new table.
UPDATE retention_daily SET d1 = NULL, d7 = NULL, d30 = NULL;

-- Currency definitions are metadata for an auditable asset ledger. Deleting a
-- definition must not cascade away balances or transaction history.
ALTER TABLE currency_balances
    DROP CONSTRAINT IF EXISTS currency_balances_currency_id_fkey;
ALTER TABLE currency_balances
    ADD CONSTRAINT currency_balances_currency_id_fkey
    FOREIGN KEY (currency_id) REFERENCES currencies(id) ON DELETE RESTRICT;

ALTER TABLE currency_ledger
    DROP CONSTRAINT IF EXISTS currency_ledger_currency_id_fkey;
ALTER TABLE currency_ledger
    ADD CONSTRAINT currency_ledger_currency_id_fkey
    FOREIGN KEY (currency_id) REFERENCES currencies(id) ON DELETE RESTRICT;
