CREATE TABLE IF NOT EXISTS announcements (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    importance TEXT NOT NULL DEFAULT 'info', -- info / warning / critical
    platform   TEXT NOT NULL DEFAULT '',     -- empty = all platforms
    channel    TEXT NOT NULL DEFAULT '',     -- empty = all channels
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_announcements_game ON announcements (game_id, id DESC);
