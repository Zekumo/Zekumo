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

var (
	ErrFriendLimit           = errors.New("friend limit reached")
	ErrFriendBlocked         = errors.New("friend request blocked")
	ErrFriendRequestConflict = errors.New("pending friend request already exists")
)

func (r FriendRequests) Create(ctx context.Context, gameID, fromID, toID string) (*FriendRequest, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialize both directions of a player pair. Without this lock, A -> B and
	// B -> A can both pass the reverse-request check and leave two pending rows.
	a, b := normalise(fromID, toID)
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"friend-request:"+gameID+":"+a+":"+b,
	); err != nil {
		return nil, err
	}
	var blocked bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM player_blocks
		   WHERE game_id=$1 AND (
		     (blocker_id=$2 AND blocked_id=$3) OR
		     (blocker_id=$3 AND blocked_id=$2)
		   ))`, gameID, fromID, toID,
	).Scan(&blocked); err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrFriendBlocked
	}

	var req FriendRequest
	err = tx.QueryRow(ctx,
		`INSERT INTO friend_requests (game_id, from_id, to_id)
		 SELECT $1, $2, $3
		 WHERE NOT EXISTS (
		   SELECT 1 FROM friend_requests
		   WHERE game_id=$1 AND from_id=$3 AND to_id=$2 AND status='pending'
		 )
		 ON CONFLICT (game_id, from_id, to_id) DO UPDATE
		 SET status='pending', created_at=now(), updated_at=now()
		 WHERE friend_requests.status='declined'
		 RETURNING id, game_id, from_id, to_id, status, created_at, updated_at`,
		gameID, fromID, toID,
	).Scan(&req.ID, &req.GameID, &req.FromID, &req.ToID, &req.Status, &req.CreatedAt, &req.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFriendRequestConflict
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
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

// AcceptAndFriend atomically accepts a request and creates the friendship.
// The per-player advisory locks serialize concurrent accepts involving either
// player, so the configured limit cannot be exceeded by racing requests.
func (r FriendRequests) AcceptAndFriend(ctx context.Context, id, gameID, recipientID string, limit int) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var fromID, toID string
	err = tx.QueryRow(ctx,
		`SELECT from_id, to_id FROM friend_requests
		 WHERE id=$1 AND game_id=$2 AND to_id=$3 AND status='pending'`,
		id, gameID, recipientID,
	).Scan(&fromID, &toID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	a, b := normalise(fromID, toID)
	for _, lockKey := range []string{
		"friend-request:" + gameID + ":" + a + ":" + b,
		"friend-limit:" + gameID + ":" + a,
		"friend-limit:" + gameID + ":" + b,
	} {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey,
		); err != nil {
			return err
		}
	}
	// Re-check and lock the request only after the pair lock. Request creation
	// takes the pair lock before touching this row, so keeping the same order
	// avoids a row-lock/advisory-lock deadlock.
	if err := tx.QueryRow(ctx,
		`SELECT from_id, to_id FROM friend_requests
		 WHERE id=$1 AND game_id=$2 AND to_id=$3 AND status='pending'
		 FOR UPDATE`, id, gameID, recipientID,
	).Scan(&fromID, &toID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}

	for _, playerID := range []string{fromID, toID} {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM friendships
			 WHERE game_id=$1 AND (player_a=$2 OR player_b=$2)`,
			gameID, playerID,
		).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return ErrFriendLimit
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO friendships (game_id, player_a, player_b)
		 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		gameID, a, b,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE friend_requests SET status='accepted', updated_at=now()
		 WHERE id=$1`, id,
	); err != nil {
		return err
	}
	// A legacy/concurrent reverse request must not remain visible after the
	// pair is friends.
	if _, err := tx.Exec(ctx,
		`UPDATE friend_requests SET status='declined', updated_at=now()
		 WHERE game_id=$1 AND from_id=$2 AND to_id=$3 AND status='pending'`,
		gameID, toID, fromID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

func (r FriendRequests) DeclineInGame(ctx context.Context, id, gameID, callerID string) error {
	tag, err := r.DB.Exec(ctx,
		`UPDATE friend_requests SET status = 'declined', updated_at = now()
		 WHERE id = $1 AND game_id = $2 AND status = 'pending' AND (from_id = $3 OR to_id = $3)`,
		id, gameID, callerID)
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

// RemoveAndCleanup atomically removes a friendship and its request history.
// It shares the pair lock with create/accept/block so an interrupted unfriend
// cannot leave stale history or race a relationship back into existence.
func (r Friendships) RemoveAndCleanup(ctx context.Context, gameID, p1, p2 string) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	a, b := normalise(p1, p2)
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"friend-request:"+gameID+":"+a+":"+b,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM friendships WHERE game_id=$1 AND player_a=$2 AND player_b=$3`,
		gameID, a, b,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM friend_requests
		 WHERE game_id=$1
		   AND ((from_id=$2 AND to_id=$3) OR (from_id=$3 AND to_id=$2))`,
		gameID, p1, p2,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

func (r Friendships) Count(ctx context.Context, gameID, playerID string) (int, error) {
	var n int
	err := r.DB.QueryRow(ctx,
		`SELECT count(*) FROM friendships WHERE game_id=$1 AND (player_a=$2 OR player_b=$2)`,
		gameID, playerID).Scan(&n)
	return n, err
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

// BlockAndCleanup records a block and atomically removes the relationship and
// request history. The pair lock serializes this with request creation and
// acceptance, so a racing accept cannot resurrect a blocked friendship.
func (r PlayerBlocks) BlockAndCleanup(ctx context.Context, gameID, blockerID, blockedID string) error {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	a, b := normalise(blockerID, blockedID)
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"friend-request:"+gameID+":"+a+":"+b,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO player_blocks (game_id, blocker_id, blocked_id)
		 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		gameID, blockerID, blockedID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM friendships
		 WHERE game_id=$1 AND player_a=$2 AND player_b=$3`,
		gameID, a, b,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM friend_requests
		 WHERE game_id=$1
		   AND ((from_id=$2 AND to_id=$3) OR (from_id=$3 AND to_id=$2))`,
		gameID, blockerID, blockedID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
