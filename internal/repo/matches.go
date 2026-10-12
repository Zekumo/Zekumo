package repo

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMatchConflict = errors.New("match already changed or idempotency key has different content")
var ErrInvalidMatch = errors.New("invalid match: key 1..128 bytes, 1..200 unique player UUIDs, metadata <=8 KiB, result <=64 KiB")

type MatchRecord struct {
	ID         int64           `json:"id,string"`
	GameID     string          `json:"game_id"`
	MatchKey   string          `json:"match_key"`
	Status     string          `json:"status"`
	Metadata   json.RawMessage `json:"metadata"`
	Result     json.RawMessage `json:"result,omitempty"`
	Revision   int64           `json:"revision"`
	Source     string          `json:"source"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	PlayerIDs  []string        `json:"player_ids"`
}

type Matches struct{ DB *pgxpool.Pool }

const matchCols = `id,game_id,match_key,status,metadata,result,revision,source,created_at,finished_at`

func scanMatch(row pgx.Row) (*MatchRecord, error) {
	var m MatchRecord
	err := row.Scan(&m.ID, &m.GameID, &m.MatchKey, &m.Status, &m.Metadata, &m.Result, &m.Revision, &m.Source, &m.CreatedAt, &m.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

func matchRoster(ctx context.Context, tx pgx.Tx, id int64) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT player_id::text FROM match_participants WHERE match_id=$1 ORDER BY player_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Create is idempotent only for the same game, key, normalized roster and metadata.
// All participants must already belong to this game; no player can write here.
func (r Matches) Create(ctx context.Context, gameID, key string, players []string, metadata json.RawMessage, source string) (*MatchRecord, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 || len(players) < 1 || len(players) > 200 {
		return nil, ErrInvalidMatch
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if len(metadata) > 8<<10 || !json.Valid(metadata) {
		return nil, ErrInvalidMatch
	}
	roster := make([]string, len(players))
	for i, p := range players {
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, ErrInvalidMatch
		}
		roster[i] = id.String()
	}
	sort.Strings(roster)
	for i := 1; i < len(roster); i++ {
		if roster[i] == roster[i-1] {
			return nil, ErrInvalidMatch
		}
	}
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM players WHERE game_id=$1 AND id=ANY($2::uuid[])`, gameID, roster).Scan(&count); err != nil {
		return nil, err
	}
	if count != len(roster) {
		return nil, ErrNotFound
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO match_records(game_id,match_key,metadata,source) VALUES($1,$2,$3,$4)
 ON CONFLICT(game_id,match_key) DO NOTHING RETURNING id`, gameID, key, metadata, source).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		m, err := scanMatch(tx.QueryRow(ctx, `SELECT `+matchCols+` FROM match_records WHERE game_id=$1 AND match_key=$2`, gameID, key))
		if err != nil {
			return nil, err
		}
		var same bool
		if err = tx.QueryRow(ctx, `SELECT metadata=$2::jsonb FROM match_records WHERE id=$1`, m.ID, metadata).Scan(&same); err != nil {
			return nil, err
		}
		m.PlayerIDs, err = matchRoster(ctx, tx, m.ID)
		if err != nil {
			return nil, err
		}
		if !same || strings.Join(m.PlayerIDs, ",") != strings.Join(roster, ",") {
			return nil, ErrMatchConflict
		}
		return m, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	// Recheck game ownership in the INSERT, in addition to validating the roster.
	tag, err := tx.Exec(ctx, `INSERT INTO match_participants(match_id,player_id)
 SELECT $1,id FROM players WHERE game_id=$2 AND id=ANY($3::uuid[])`, id, gameID, roster)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != int64(len(roster)) {
		return nil, ErrNotFound
	}
	m, err := scanMatch(tx.QueryRow(ctx, `SELECT `+matchCols+` FROM match_records WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	m.PlayerIDs = roster
	return m, tx.Commit(ctx)
}

func (r Matches) Get(ctx context.Context, gameID string, id int64, playerID string) (*MatchRecord, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	m, err := scanMatch(tx.QueryRow(ctx, `SELECT `+matchCols+` FROM match_records m WHERE game_id=$1 AND id=$2
 AND ($3='' OR EXISTS(SELECT 1 FROM match_participants p WHERE p.match_id=m.id AND p.player_id::text=$3))`, gameID, id, playerID))
	if err != nil {
		return nil, err
	}
	m.PlayerIDs, err = matchRoster(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return m, tx.Commit(ctx)
}

// List uses ID cursor pagination. Roster is returned only by Get, avoiding N+1 queries.
func (r Matches) List(ctx context.Context, gameID, playerID string, before int64, limit int) ([]MatchRecord, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := r.DB.Query(ctx, `SELECT `+matchCols+` FROM match_records m WHERE game_id=$1
 AND ($2='' OR EXISTS(SELECT 1 FROM match_participants p WHERE p.match_id=m.id AND p.player_id::text=$2))
 AND ($3::bigint=0 OR id<$3) ORDER BY id DESC LIMIT $4`, gameID, playerID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MatchRecord{}
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// Finish makes the result immutable. Exact replay succeeds; different terminal
// content or stale revision conflicts. Completion never trusts a player claim.
func (r Matches) Finish(ctx context.Context, gameID string, id, expected int64, status string, result json.RawMessage) (*MatchRecord, error) {
	if expected < 1 || (status != "completed" && status != "cancelled") || len(result) == 0 || len(result) > 64<<10 || !json.Valid(result) {
		return nil, ErrInvalidMatch
	}
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	m, err := scanMatch(tx.QueryRow(ctx, `SELECT `+matchCols+` FROM match_records WHERE game_id=$1 AND id=$2 FOR UPDATE`, gameID, id))
	if err != nil {
		return nil, err
	}
	if m.Status != "active" {
		var same bool
		if err = tx.QueryRow(ctx, `SELECT status=$2 AND result=$3::jsonb FROM match_records WHERE id=$1`, id, status, result).Scan(&same); err != nil {
			return nil, err
		}
		if !same {
			return nil, ErrMatchConflict
		}
	} else {
		if m.Revision != expected {
			return nil, ErrMatchConflict
		}
		m, err = scanMatch(tx.QueryRow(ctx, `UPDATE match_records SET status=$2,result=$3,revision=revision+1,finished_at=now() WHERE id=$1 RETURNING `+matchCols, id, status, result))
		if err != nil {
			return nil, err
		}
	}
	m.PlayerIDs, err = matchRoster(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return m, tx.Commit(ctx)
}
