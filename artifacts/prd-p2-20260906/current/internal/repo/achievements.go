package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AchievementDefs manages the game-level achievement catalogue.
type AchievementDefs struct{ DB *pgxpool.Pool }

var ErrAchievementUnlocked = errors.New("achievement has unlocked players")

const achDefCols = `id, game_id, key, name, description, icon_url, rarity, hidden, type, target, sort_order, created_at`

func scanAchDef(row pgx.Row) (*AchievementDef, error) {
	var d AchievementDef
	err := row.Scan(&d.ID, &d.GameID, &d.Key, &d.Name, &d.Description, &d.IconURL,
		&d.Rarity, &d.Hidden, &d.Type, &d.Target, &d.SortOrder, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

func (r AchievementDefs) Create(ctx context.Context, gameID string, d AchievementDef) (*AchievementDef, error) {
	return scanAchDef(r.DB.QueryRow(ctx,
		`INSERT INTO achievement_defs (game_id, key, name, description, icon_url, rarity, hidden, type, target, sort_order)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+achDefCols,
		gameID, d.Key, d.Name, d.Description, d.IconURL, d.Rarity, d.Hidden, d.Type, d.Target, d.SortOrder))
}

func (r AchievementDefs) Update(ctx context.Context, id string, d AchievementDef) (*AchievementDef, error) {
	return scanAchDef(r.DB.QueryRow(ctx,
		`UPDATE achievement_defs
		 SET name=$2, description=$3, icon_url=$4, rarity=$5, hidden=$6, type=$7, target=$8, sort_order=$9
		 WHERE id=$1 RETURNING `+achDefCols,
		id, d.Name, d.Description, d.IconURL, d.Rarity, d.Hidden, d.Type, d.Target, d.SortOrder))
}

func (r AchievementDefs) Delete(ctx context.Context, id string) error {
	tag, err := r.DB.Exec(ctx,
		`DELETE FROM achievement_defs d
		 WHERE d.id=$1
		   AND NOT EXISTS (
		     SELECT 1 FROM achievement_unlocks u
		     WHERE u.achievement_id=d.id AND u.unlocked_at IS NOT NULL
		   )`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := r.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM achievement_defs WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrAchievementUnlocked
}

func (r AchievementDefs) ByID(ctx context.Context, id string) (*AchievementDef, error) {
	return scanAchDef(r.DB.QueryRow(ctx,
		`SELECT `+achDefCols+` FROM achievement_defs WHERE id=$1`, id))
}

func (r AchievementDefs) ByGame(ctx context.Context, gameID string) ([]AchievementDef, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT `+achDefCols+` FROM achievement_defs WHERE game_id=$1 ORDER BY sort_order, created_at`,
		gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AchievementDef
	for rows.Next() {
		d, err := scanAchDef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// AchievementUnlocks manages per-player unlock state.
type AchievementUnlocks struct{ DB *pgxpool.Pool }

// Upsert records progress toward an achievement. When progress >= target the
// achievement is unlocked (unlocked_at set to now on first unlock, preserved
// thereafter). Safe to call multiple times — only advances, never regresses.
func (r AchievementUnlocks) Upsert(ctx context.Context, achID, playerID string, progress int) (*AchievementUnlock, error) {
	u, _, err := r.UpsertWithTransition(ctx, achID, playerID, progress)
	return u, err
}

// UpsertWithTransition also reports whether this call changed the achievement
// from locked to unlocked. The advisory transaction lock keeps that transition
// exact for concurrent duplicate requests, so callers emit one webhook only.
func (r AchievementUnlocks) UpsertWithTransition(ctx context.Context, achID, playerID string, progress int) (*AchievementUnlock, bool, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1 || ':' || $2, 0))`, achID, playerID); err != nil {
		return nil, false, err
	}
	var wasUnlocked bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM achievement_unlocks
		   WHERE achievement_id=$1 AND player_id=$2 AND unlocked_at IS NOT NULL
		 )`, achID, playerID).Scan(&wasUnlocked); err != nil {
		return nil, false, err
	}

	var u AchievementUnlock
	err = tx.QueryRow(ctx,
		`WITH def AS (SELECT target, game_id FROM achievement_defs WHERE id=$1)
		 INSERT INTO achievement_unlocks (achievement_id, player_id, progress, unlocked_at)
		 SELECT $1, $2, $3,
		        CASE WHEN $3 >= def.target THEN now() ELSE NULL END
		 FROM def
		 JOIN players p ON p.id=$2 AND p.game_id=def.game_id
		 ON CONFLICT (achievement_id, player_id) DO UPDATE
		   SET progress    = GREATEST(achievement_unlocks.progress, EXCLUDED.progress),
		       unlocked_at = CASE
		         WHEN achievement_unlocks.unlocked_at IS NOT NULL THEN achievement_unlocks.unlocked_at
		         WHEN GREATEST(achievement_unlocks.progress, EXCLUDED.progress) >= (SELECT target FROM def)
		              THEN now()
		         ELSE NULL
		       END
		 RETURNING achievement_id, player_id, progress, unlocked_at`,
		achID, playerID, progress,
	).Scan(&u.AchievementID, &u.PlayerID, &u.Progress, &u.UnlockedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &u, !wasUnlocked && u.UnlockedAt != nil, nil
}

// ByPlayer returns all unlock rows for a player (both locked-with-progress and unlocked).
func (r AchievementUnlocks) ByPlayer(ctx context.Context, playerID string) ([]AchievementUnlock, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT achievement_id, player_id, progress, unlocked_at
		 FROM achievement_unlocks WHERE player_id=$1`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AchievementUnlock
	for rows.Next() {
		var u AchievementUnlock
		if err := rows.Scan(&u.AchievementID, &u.PlayerID, &u.Progress, &u.UnlockedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnlockedByPlayer returns only the fully-unlocked entries for a player.
func (r AchievementUnlocks) UnlockedByPlayer(ctx context.Context, playerID string) ([]AchievementUnlock, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT achievement_id, player_id, progress, unlocked_at
		 FROM achievement_unlocks WHERE player_id=$1 AND unlocked_at IS NOT NULL
		 ORDER BY unlocked_at DESC`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AchievementUnlock
	for rows.Next() {
		var u AchievementUnlock
		if err := rows.Scan(&u.AchievementID, &u.PlayerID, &u.Progress, &u.UnlockedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnlockMap returns achievement_id -> AchievementUnlock for a player (fast lookup).
func (r AchievementUnlocks) UnlockMap(ctx context.Context, playerID string) (map[string]AchievementUnlock, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT achievement_id, player_id, progress, unlocked_at
		 FROM achievement_unlocks WHERE player_id=$1`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AchievementUnlock{}
	for rows.Next() {
		var u AchievementUnlock
		if err := rows.Scan(&u.AchievementID, &u.PlayerID, &u.Progress, &u.UnlockedAt); err != nil {
			return nil, err
		}
		out[u.AchievementID] = u
	}
	return out, rows.Err()
}
