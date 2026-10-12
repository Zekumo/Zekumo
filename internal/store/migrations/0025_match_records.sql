-- Durable trusted-server match records, separate from ephemeral WS rooms.
CREATE TABLE match_records (
 id BIGSERIAL PRIMARY KEY,
 game_id UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
 match_key TEXT NOT NULL CHECK(length(match_key) BETWEEN 1 AND 128),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','completed','cancelled')),
 metadata JSONB NOT NULL DEFAULT '{}',
 result JSONB,
 revision BIGINT NOT NULL DEFAULT 1,
 source TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 finished_at TIMESTAMPTZ,
 UNIQUE(game_id,match_key)
);
CREATE INDEX match_records_game_recent ON match_records(game_id,id DESC);
CREATE TABLE match_participants (
 match_id BIGINT NOT NULL REFERENCES match_records(id) ON DELETE CASCADE,
 player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 PRIMARY KEY(match_id,player_id)
);
CREATE INDEX match_participants_player ON match_participants(player_id,match_id DESC);
