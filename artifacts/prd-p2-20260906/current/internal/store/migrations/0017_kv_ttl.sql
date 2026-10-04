ALTER TABLE game_data ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_game_data_expiry ON game_data (expires_at) WHERE expires_at IS NOT NULL;
