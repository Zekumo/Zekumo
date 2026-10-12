package repo

import (
	"context"
	"time"
)

type FunctionSchedule struct {
	NextRunAt        time.Time  `json:"next_run_at"`
	LastDispatchedAt *time.Time `json:"last_dispatched_at"`
	DispatchCount    int64      `json:"dispatch_count"`
}

// ClaimCron atomically advances persisted due times before returning work.
// SKIP LOCKED shares dispatches across replicas without holding DB locks while
// executing untrusted code. A crash after claiming loses that interval, so game
// effects must still use their own idempotency keys; this is not exactly-once.
func (r Functions) ClaimCron(ctx context.Context, limit int) ([]CloudFunction, error) {
	if limit < 1 || limit > 32 {
		limit = 32
	}
	rows, err := r.DB.Query(ctx, `WITH due AS (
 SELECT s.function_id,f.cron_secs FROM cloud_function_schedules s
 JOIN cloud_functions f ON f.id=s.function_id
 WHERE s.next_run_at<=now() AND f.enabled AND f.cron_secs>0
 ORDER BY s.next_run_at,s.function_id FOR UPDATE OF s SKIP LOCKED LIMIT $1
 ), claimed AS (
 UPDATE cloud_function_schedules s SET next_run_at=now()+make_interval(secs=>due.cron_secs),
 last_dispatched_at=now(),dispatch_count=s.dispatch_count+1 FROM due WHERE s.function_id=due.function_id
 RETURNING s.function_id)
 SELECT f.id,f.game_id,f.name,f.code,f.enabled,f.public,f.cron_secs,f.updated_at,f.revision
 FROM cloud_functions f JOIN claimed c ON c.function_id=f.id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudFunction{}
	for rows.Next() {
		f, err := scanFn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

func (r Functions) Schedule(ctx context.Context, gameID, name string) (*FunctionSchedule, error) {
	var s FunctionSchedule
	err := r.DB.QueryRow(ctx, `SELECT s.next_run_at,s.last_dispatched_at,s.dispatch_count
 FROM cloud_function_schedules s JOIN cloud_functions f ON f.id=s.function_id
 WHERE f.game_id=$1 AND f.name=$2`, gameID, name).Scan(&s.NextRunAt, &s.LastDispatchedAt, &s.DispatchCount)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
