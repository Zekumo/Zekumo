CREATE TABLE IF NOT EXISTS export_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id     UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL,
    player_id   UUID,
    format      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    error       TEXT NOT NULL DEFAULT '',
    storage_key TEXT NOT NULL DEFAULT '',
    size        BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_export_jobs_game ON export_jobs (game_id, created_at DESC);
