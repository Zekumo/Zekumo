CREATE TABLE IF NOT EXISTS accounts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    nickname      TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE players ADD COLUMN IF NOT EXISTS account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_players_account ON players (game_id, account_id) WHERE account_id IS NOT NULL;
ALTER TABLE games ADD COLUMN IF NOT EXISTS sso_redirect_urls TEXT NOT NULL DEFAULT '';
