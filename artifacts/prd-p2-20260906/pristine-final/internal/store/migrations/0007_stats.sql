CREATE TABLE IF NOT EXISTS stats_daily (
    game_id     UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    date        DATE NOT NULL,
    active      INT NOT NULL DEFAULT 0,
    logins      INT NOT NULL DEFAULT 0,
    new_players INT NOT NULL DEFAULT 0,
    PRIMARY KEY (game_id, date)
);
