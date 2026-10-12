-- Daily facts enable exact calendar-day cohort retention. Existing historical
-- approximations remain stored but are not relabelled as exact measurements.
CREATE TABLE player_activity_daily (
 game_id UUID NOT NULL REFERENCES games(id) ON DELETE CASCADE,
 player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 timezone TEXT NOT NULL,
 activity_date DATE NOT NULL,
 PRIMARY KEY(game_id,player_id,timezone,activity_date)
);
CREATE INDEX player_activity_daily_date ON player_activity_daily(activity_date);
CREATE TABLE stats_activity_tracking (
 timezone TEXT PRIMARY KEY,
 started_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE retention_daily ADD COLUMN measurement TEXT NOT NULL DEFAULT 'legacy_cumulative';
ALTER TABLE retention_daily ADD COLUMN stats_timezone TEXT NOT NULL DEFAULT '';
ALTER TABLE retention_daily ADD COLUMN returned7 INT;
