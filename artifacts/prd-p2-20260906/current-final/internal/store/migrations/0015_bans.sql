CREATE TABLE IF NOT EXISTS player_bans (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id  UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    reason     TEXT NOT NULL,
    operator   TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lifted_at  TIMESTAMPTZ,
    lifted_by  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_player_bans_player ON player_bans (player_id) WHERE lifted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_player_bans_game ON player_bans (game_id, created_at DESC);
