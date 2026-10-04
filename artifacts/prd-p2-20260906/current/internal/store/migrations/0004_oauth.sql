CREATE TABLE IF NOT EXISTS oauth_clients (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id     TEXT UNIQUE NOT NULL,
    client_secret TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL,
    redirect_urls TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oauth_grants (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    client_pk  UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    scope      TEXT NOT NULL DEFAULT 'profile',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, client_pk)
);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash TEXT UNIQUE NOT NULL,
    family     UUID NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    client_pk  UUID NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    scope      TEXT NOT NULL DEFAULT 'profile',
    used       BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_oauth_rt_family ON oauth_refresh_tokens (family);
CREATE INDEX IF NOT EXISTS idx_oauth_rt_pair ON oauth_refresh_tokens (account_id, client_pk);
