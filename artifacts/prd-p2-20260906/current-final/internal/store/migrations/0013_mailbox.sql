CREATE TABLE IF NOT EXISTS mail_messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_id    UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    broadcast  BOOLEAN NOT NULL DEFAULT FALSE,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    rewards    JSONB NOT NULL DEFAULT '[]',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_mail_messages_game ON mail_messages (game_id, created_at DESC);

CREATE TABLE IF NOT EXISTS mail_recipients (
    mail_id    UUID NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
    player_id  UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    read_at    TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    PRIMARY KEY (mail_id, player_id)
);
CREATE INDEX IF NOT EXISTS idx_mail_recipients_player ON mail_recipients (player_id);
