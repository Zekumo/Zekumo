package ratelimit

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Attempts locks an identity out after too many consecutive failures. The
// window slides: every new failure extends the lockout, so a slow grinding
// attack cannot wait out a fixed window.
type Attempts struct {
	RDB    *redis.Client
	Max    int
	Window time.Duration
}

func NewAttempts(rdb *redis.Client) Attempts {
	return Attempts{RDB: rdb, Max: 10, Window: 15 * time.Minute}
}

func (a Attempts) key(identity string) string { return "lockout:" + identity }

// Fail records a failure and reports whether the identity is now locked.
func (a Attempts) Fail(ctx context.Context, identity string) bool {
	pipe := a.RDB.Pipeline()
	count := pipe.Incr(ctx, a.key(identity))
	pipe.Expire(ctx, a.key(identity), a.Window)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("ratelimit: lockout update failed: %v", err)
		return false
	}
	return count.Val() >= int64(a.Max)
}

// Locked reports whether the identity is currently locked out.
func (a Attempts) Locked(ctx context.Context, identity string) bool {
	n, err := a.RDB.Get(ctx, a.key(identity)).Int64()
	if err != nil { // missing key or Redis trouble: do not lock people out
		return false
	}
	return n >= int64(a.Max)
}

// Succeed clears the failure count after a successful login.
func (a Attempts) Succeed(ctx context.Context, identity string) {
	a.RDB.Del(ctx, a.key(identity))
}
