CREATE TABLE IF NOT EXISTS achievement_defs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id     UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    key         TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    icon_url    TEXT NOT NULL DEFAULT '',
    rarity      TEXT NOT NULL DEFAULT 'common', -- common / rare / epic / legendary
    hidden      BOOLEAN NOT NULL DEFAULT FALSE,
    type        TEXT NOT NULL DEFAULT 'instant', -- instant / progress
    target      INT NOT NULL DEFAULT 1,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, key)
);

CREATE TABLE IF NOT EXISTS achievement_unlocks (
    achievement_id UUID NOT NULL REFERENCES achievement_defs(id) ON DELETE CASCADE,
    player_id      UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    progress       INT NOT NULL DEFAULT 0,
    unlocked_at    TIMESTAMPTZ,
    PRIMARY KEY (achievement_id, player_id)
);
CREATE INDEX IF NOT EXISTS idx_achievement_unlocks_player ON achievement_unlocks (player_id);
