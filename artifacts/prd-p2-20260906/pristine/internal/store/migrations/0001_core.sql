CREATE TABLE IF NOT EXISTS games (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id     TEXT UNIQUE NOT NULL,
    app_secret TEXT NOT NULL,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS players (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id       UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    identifier    TEXT NOT NULL,
    password_hash TEXT,
    nickname      TEXT NOT NULL DEFAULT '',
    profile       JSONB NOT NULL DEFAULT '{}',
    banned        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, provider, identifier)
);

CREATE TABLE IF NOT EXISTS player_data (
    player_id  UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (player_id, key)
);

CREATE TABLE IF NOT EXISTS scores (
    game_id    UUID NOT NULL,
    board      TEXT NOT NULL,
    player_id  UUID NOT NULL,
    score      BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, board, player_id)
);

CREATE TABLE IF NOT EXISTS dialogue_scripts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    script_key TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    content    JSONB NOT NULL DEFAULT '[]',
    version    INT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, script_key)
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id          BIGSERIAL PRIMARY KEY,
    game_id     UUID NOT NULL,
    channel     TEXT NOT NULL,
    sender_id   UUID NOT NULL,
    sender_name TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chat_channel ON chat_messages (game_id, channel, id DESC);
