CREATE TABLE IF NOT EXISTS cloud_functions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    code       TEXT NOT NULL DEFAULT '',
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    public     BOOLEAN NOT NULL DEFAULT FALSE,
    cron_secs  INT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, name)
);

CREATE TABLE IF NOT EXISTS game_data (
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, key)
);
