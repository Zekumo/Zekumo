ALTER TABLE games ADD COLUMN IF NOT EXISTS friend_limit INT NOT NULL DEFAULT 200;
ALTER TABLE games DROP CONSTRAINT IF EXISTS games_friend_limit_check;
ALTER TABLE games ADD CONSTRAINT games_friend_limit_check CHECK (friend_limit BETWEEN 1 AND 10000);
