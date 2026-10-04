CREATE TABLE IF NOT EXISTS friend_requests (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    from_id    UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    to_id      UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    status     TEXT NOT NULL DEFAULT 'pending', -- pending / accepted / declined
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, from_id, to_id)
);
CREATE INDEX IF NOT EXISTS idx_friend_req_to ON friend_requests (game_id, to_id, status);

-- Canonical ordering: player_a < player_b (UUID lexicographic) avoids duplicate rows.
-- Always normalise before insert/query: a=least(p1,p2), b=greatest(p1,p2).
CREATE TABLE IF NOT EXISTS friendships (
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_a   UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    player_b   UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, player_a, player_b),
    CHECK (player_a < player_b)
);
CREATE INDEX IF NOT EXISTS idx_friendships_b ON friendships (game_id, player_b);

CREATE TABLE IF NOT EXISTS player_blocks (
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    blocker_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    blocked_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, blocker_id, blocked_id)
);
