ALTER TABLE games ADD COLUMN IF NOT EXISTS func_http_allowlist TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS app_logs (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id    UUID,
    level      TEXT NOT NULL,
    source     TEXT NOT NULL,
    event      TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL,
    fields     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_app_logs_game ON app_logs (game_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_app_logs_created ON app_logs (created_at);
