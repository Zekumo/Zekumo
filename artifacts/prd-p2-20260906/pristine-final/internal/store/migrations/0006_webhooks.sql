CREATE TABLE IF NOT EXISTS webhooks (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    url        TEXT NOT NULL,
    secret     TEXT NOT NULL,
    events     TEXT NOT NULL DEFAULT '*',
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    webhook_id UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event      TEXT NOT NULL,
    payload    JSONB NOT NULL,
    status     INT NOT NULL DEFAULT 0,
    attempts   INT NOT NULL DEFAULT 0,
    ok         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries ON webhook_deliveries (webhook_id, id DESC);
