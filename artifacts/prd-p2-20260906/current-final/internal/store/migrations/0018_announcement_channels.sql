ALTER TABLE announcements ADD COLUMN IF NOT EXISTS channel TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_announcements_target ON announcements (game_id, platform, channel, created_at);
