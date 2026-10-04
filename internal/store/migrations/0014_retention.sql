CREATE TABLE IF NOT EXISTS retention_daily (
    game_id UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    date    DATE NOT NULL,
    cohort  INT NOT NULL DEFAULT 0,
    d1      INT,
    d7      INT,
    d30     INT,
    PRIMARY KEY (game_id, date)
);
