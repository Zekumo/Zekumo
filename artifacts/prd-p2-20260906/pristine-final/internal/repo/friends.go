package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Friend Requests ──────────────────────────────────────────────────────────

type FriendRequests struct{ DB *pgxpool.Pool }

func (r FriendRequests) Create(ctx context.Context, gameID, fromID, toID string) (*FriendRequest, error) {
	var req FriendRequest
	err := r.DB.QueryRow(ctx,
		`INSERT INTO friend_requests (game_id, from_id, to_id)
		 VALUES ($1, $2, $3)
		 RETURNING id, game_id, from_id, to_id, status, created_at, updated_at`,
		gameID, fromID, toID,
	).Scan(&req.ID, &req.GameID, &req.FromID, &req.ToID, &req.Status, &req.CreatedAt, &req.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (r FriendRequests) ByID(ctx context.Context, id string) (*FriendRequest, error) {
	var req FriendRequest
	err := r.DB.QueryRow(ctx,
		`SELECT id, game_id, from_id, to_id, status, created_at, updated_at
		 FROM friend_requests WHERE id = $1`, id,
	).Scan(&req.ID, &req.GameID, &req.FromID, &req.ToID, &req.Status, &req.CreatedAt, &req.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &req, err
}

// Accept transitions a pending request to accepted.
func (r FriendRequests) Accept(ctx context.Context, id string) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE friend_requests SET status = 'accepted', updated_at = now()
		 WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Decline transitions a pending request to declined (or retracts an outgoing one).
func (r FriendRequests) Decline(ctx context.Context, id, callerID string) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE friend_requests SET status = 'declined', updated_at = now()
		 WHERE id = $1 AND status = 'pending' AND (from_id = $2 OR to_id = $2)`,
		id, callerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteBetween removes all request history between two players. It is called
// when they unfriend or block each other so a later request can start fresh.
func (r FriendRequests) DeleteBetween(ctx context.Context, gameID, p1, p2 string) error {
	_, err := r.DB.Exec(ctx,
		`DELETE FROM friend_requests
		 WHERE game_id = $1
		   AND ((from_id = $2 AND to_id = $3) OR (from_id = $3 AND to_id = $2))`,
		gameID, p1, p2)
	return err
}

// List returns pending requests where the player is either sender or recipient,
// enriched with the other party's nickname.
func (r FriendRequests) List(ctx context.Context, gameID, playerID string) ([]FriendRequest, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT fr.id, fr.game_id, fr.from_id, fr.to_id, fr.status, fr.created_at, fr.updated_at,
		        pf.nickname, pt.nickname
		 FROM friend_requests fr
		 JOIN players pf ON pf.id = fr.from_id
		 JOIN players pt ON pt.id = fr.to_id
		 WHERE fr.game_id = $1 AND fr.status = 'pending'
		   AND (fr.from_id = $2 OR fr.to_id = $2)
		 ORDER BY fr.created_at DESC`,
		gameID, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FriendRequest
	for rows.Next() {
		var req FriendRequest
		if err := rows.Scan(&req.ID, &req.GameID, &req.FromID, &req.ToID, &req.Status,
			&req.CreatedAt, &req.UpdatedAt, &req.FromNickname, &req.ToNickname); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

// ── Friendships ───────────────────────────────────────────────────────────────

type Friendships struct{ DB *pgxpool.Pool }

// normalise returns (a, b) with a <= b so the CHECK constraint is always met.
func normalise(p1, p2 string) (string, string) {
	if p1 < p2 {
		return p1, p2
	}
	return p2, p1
}

func (r Friendships) Add(ctx context.Context, gameID, p1, p2 string) error {
	a, b := normalise(p1, p2)
	_, err := r.DB.Exec(ctx,
		`INSERT INTO friendships (game_id, player_a, player_b) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`,
		gameID, a, b)
	return err
}

func (r Friendships) Remove(ctx context.Context, gameID, p1, p2 string) error {
	a, b := normalise(p1, p2)
	_, err := r.DB.Exec(ctx,
		`DELETE FROM friendships WHERE game_id = $1 AND player_a = $2 AND player_b = $3`,
		gameID, a, b)
	return err
}

// List returns the IDs of all friends of playerID in the game,
// along with their nicknames and the friendship creation time.
func (r Friendships) List(ctx context.Context, gameID, playerID string) ([]friendRow, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT
		   CASE WHEN f.player_a = $2 THEN f.player_b ELSE f.player_a END AS friend_id,
		   p.nickname,
		   f.created_at
		 FROM friendships f
		 JOIN players p ON p.id = CASE WHEN f.player_a = $2 THEN f.player_b ELSE f.player_a END
		 WHERE f.game_id = $1 AND (f.player_a = $2 OR f.player_b = $2)
		 ORDER BY f.created_at DESC`,
		gameID, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []friendRow
	for rows.Next() {
		var fr friendRow
		if err := rows.Scan(&fr.PlayerID, &fr.Nickname, &fr.FriendSince); err != nil {
			return nil, err
		}
		out = append(out, fr)
	}
	return out, rows.Err()
}

type friendRow struct {
	PlayerID    string
	Nickname    string
	FriendSince time.Time
}

// IDs returns just the friend player IDs (used for leaderboard scope=friends).
func (r Friendships) IDs(ctx context.Context, gameID, playerID string) ([]string, error) {
	rows, err := r.DB.Query(ctx,
		`SELECT CASE WHEN player_a = $2 THEN player_b ELSE player_a END
		 FROM friendships WHERE game_id = $1 AND (player_a = $2 OR player_b = $2)`,
		gameID, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r Friendships) Exists(ctx context.Context, gameID, p1, p2 string) (bool, error) {
	a, b := normalise(p1, p2)
	var exists bool
	err := r.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM friendships WHERE game_id=$1 AND player_a=$2 AND player_b=$3)`,
		gameID, a, b).Scan(&exists)
	return exists, err
}

// ── Player Blocks ─────────────────────────────────────────────────────────────

type PlayerBlocks struct{ DB *pgxpool.Pool }

func (r PlayerBlocks) Block(ctx context.Context, gameID, blockerID, blockedID string) error {
	_, err := r.DB.Exec(ctx,
		`INSERT INTO player_blocks (game_id, blocker_id, blocked_id) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`,
		gameID, blockerID, blockedID)
	return err
}

func (r PlayerBlocks) Unblock(ctx context.Context, gameID, blockerID, blockedID string) error {
	_, err := r.DB.Exec(ctx,
		`DELETE FROM player_blocks WHERE game_id=$1 AND blocker_id=$2 AND blocked_id=$3`,
		gameID, blockerID, blockedID)
	return err
}

func (r PlayerBlocks) IsBlocked(ctx context.Context, gameID, blockerID, blockedID string) (bool, error) {
	var exists bool
	err := r.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM player_blocks WHERE game_id=$1 AND blocker_id=$2 AND blocked_id=$3)`,
		gameID, blockerID, blockedID).Scan(&exists)
	return exists, err
}

// EitherBlocked reports whether either party has blocked the other.
func (r PlayerBlocks) EitherBlocked(ctx context.Context, gameID, p1, p2 string) (bool, error) {
	var exists bool
	err := r.DB.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM player_blocks
		   WHERE game_id=$1 AND (
		     (blocker_id=$2 AND blocked_id=$3) OR
		     (blocker_id=$3 AND blocked_id=$2)
		   ))`,
		gameID, p1, p2).Scan(&exists)
	return exists, err
}
