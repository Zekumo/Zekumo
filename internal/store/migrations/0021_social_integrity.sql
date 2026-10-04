-- Phase 2 social integrity hardening. Published migrations 0009-0011 are
-- immutable, so all constraints and indexes are added forward-only here.
-- Fail with a targeted diagnostic instead of silently rewriting operator data.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM friend_requests
        WHERE status NOT IN ('pending', 'accepted', 'declined') OR from_id = to_id
    ) THEN
        RAISE EXCEPTION '0021_social_integrity: invalid or self friend_requests exist'
            USING HINT = 'Correct the listed request rows before retrying the migration.';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM friend_requests a
        JOIN friend_requests b
          ON b.game_id = a.game_id
         AND b.from_id = a.to_id
         AND b.to_id = a.from_id
         AND b.status = 'pending'
         AND b.id > a.id
        WHERE a.status = 'pending'
    ) THEN
        RAISE EXCEPTION '0021_social_integrity: reversed pending friend requests exist'
            USING HINT = 'Decline one request in each reversed pair, then retry the migration.';
    END IF;

    IF EXISTS (SELECT 1 FROM player_blocks WHERE blocker_id = blocked_id) THEN
        RAISE EXCEPTION '0021_social_integrity: self-block rows exist'
            USING HINT = 'Remove self-block rows before retrying the migration.';
    END IF;

    IF EXISTS (
        SELECT 1 FROM achievement_defs
        WHERE rarity NOT IN ('common', 'rare', 'epic', 'legendary')
           OR type NOT IN ('instant', 'progress')
           OR target < 1
    ) THEN
        RAISE EXCEPTION '0021_social_integrity: invalid achievement definitions exist'
            USING HINT = 'Correct rarity, type, and target values before retrying the migration.';
    END IF;

    IF EXISTS (
        SELECT 1 FROM announcements
        WHERE importance NOT IN ('info', 'warning', 'critical')
    ) THEN
        RAISE EXCEPTION '0021_social_integrity: invalid announcement importance exists'
            USING HINT = 'Correct announcement importance values before retrying the migration.';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM friend_requests fr
        JOIN players pf ON pf.id = fr.from_id
        JOIN players pt ON pt.id = fr.to_id
        WHERE pf.game_id <> fr.game_id OR pt.game_id <> fr.game_id
    ) OR EXISTS (
        SELECT 1
        FROM friendships f
        JOIN players pa ON pa.id = f.player_a
        JOIN players pb ON pb.id = f.player_b
        WHERE pa.game_id <> f.game_id OR pb.game_id <> f.game_id
    ) OR EXISTS (
        SELECT 1
        FROM player_blocks b
        JOIN players blocker ON blocker.id = b.blocker_id
        JOIN players blocked ON blocked.id = b.blocked_id
        WHERE blocker.game_id <> b.game_id OR blocked.game_id <> b.game_id
    ) THEN
        RAISE EXCEPTION '0021_social_integrity: cross-game social rows exist'
            USING HINT = 'Move or remove cross-game relationships before retrying the migration.';
    END IF;
END $$;

ALTER TABLE friend_requests
    ADD CONSTRAINT friend_requests_status_check
    CHECK (status IN ('pending', 'accepted', 'declined')) NOT VALID,
    ADD CONSTRAINT friend_requests_no_self_check
    CHECK (from_id <> to_id) NOT VALID;
ALTER TABLE friend_requests VALIDATE CONSTRAINT friend_requests_status_check;
ALTER TABLE friend_requests VALIDATE CONSTRAINT friend_requests_no_self_check;

ALTER TABLE player_blocks
    ADD CONSTRAINT player_blocks_no_self_check
    CHECK (blocker_id <> blocked_id) NOT VALID;
ALTER TABLE player_blocks VALIDATE CONSTRAINT player_blocks_no_self_check;

ALTER TABLE achievement_defs
    ADD CONSTRAINT achievement_defs_rarity_check
    CHECK (rarity IN ('common', 'rare', 'epic', 'legendary')) NOT VALID,
    ADD CONSTRAINT achievement_defs_type_check
    CHECK (type IN ('instant', 'progress')) NOT VALID,
    ADD CONSTRAINT achievement_defs_target_check
    CHECK (target >= 1) NOT VALID;
ALTER TABLE achievement_defs VALIDATE CONSTRAINT achievement_defs_rarity_check;
ALTER TABLE achievement_defs VALIDATE CONSTRAINT achievement_defs_type_check;
ALTER TABLE achievement_defs VALIDATE CONSTRAINT achievement_defs_target_check;

ALTER TABLE announcements
    ADD CONSTRAINT announcements_importance_check
    CHECK (importance IN ('info', 'warning', 'critical')) NOT VALID;
ALTER TABLE announcements VALIDATE CONSTRAINT announcements_importance_check;

-- Enforce one pending request per unordered player pair, regardless of which
-- player initiated it.
CREATE UNIQUE INDEX uq_friend_requests_pending_pair
    ON friend_requests (game_id, (LEAST(from_id, to_id)), (GREATEST(from_id, to_id)))
    WHERE status = 'pending';

-- Composite foreign keys keep the denormalized game_id aligned with every
-- referenced player, preventing cross-tenant relationships from direct SQL.
CREATE UNIQUE INDEX IF NOT EXISTS uq_players_game_id_id ON players (game_id, id);

ALTER TABLE friend_requests
    ADD CONSTRAINT friend_requests_from_game_fk
    FOREIGN KEY (game_id, from_id) REFERENCES players (game_id, id) ON DELETE CASCADE NOT VALID,
    ADD CONSTRAINT friend_requests_to_game_fk
    FOREIGN KEY (game_id, to_id) REFERENCES players (game_id, id) ON DELETE CASCADE NOT VALID;
ALTER TABLE friend_requests VALIDATE CONSTRAINT friend_requests_from_game_fk;
ALTER TABLE friend_requests VALIDATE CONSTRAINT friend_requests_to_game_fk;

ALTER TABLE friendships
    ADD CONSTRAINT friendships_a_game_fk
    FOREIGN KEY (game_id, player_a) REFERENCES players (game_id, id) ON DELETE CASCADE NOT VALID,
    ADD CONSTRAINT friendships_b_game_fk
    FOREIGN KEY (game_id, player_b) REFERENCES players (game_id, id) ON DELETE CASCADE NOT VALID;
ALTER TABLE friendships VALIDATE CONSTRAINT friendships_a_game_fk;
ALTER TABLE friendships VALIDATE CONSTRAINT friendships_b_game_fk;

ALTER TABLE player_blocks
    ADD CONSTRAINT player_blocks_blocker_game_fk
    FOREIGN KEY (game_id, blocker_id) REFERENCES players (game_id, id) ON DELETE CASCADE NOT VALID,
    ADD CONSTRAINT player_blocks_blocked_game_fk
    FOREIGN KEY (game_id, blocked_id) REFERENCES players (game_id, id) ON DELETE CASCADE NOT VALID;
ALTER TABLE player_blocks VALIDATE CONSTRAINT player_blocks_blocker_game_fk;
ALTER TABLE player_blocks VALIDATE CONSTRAINT player_blocks_blocked_game_fk;
