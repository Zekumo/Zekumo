package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	DB  *pgxpool.Pool
	RDB *redis.Client
}

func Open(ctx context.Context, databaseURL, redisAddr string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Store{DB: pool, RDB: rdb}, nil
}

func (s *Store) Close() {
	s.DB.Close()
	_ = s.RDB.Close()
}
