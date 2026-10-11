package store

import (
	"sync"
	"testing"
)

func TestConcurrentMigrationStartup(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	const workers = 6
	var wg sync.WaitGroup
	errors := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errors <- Migrate(ctx, db)
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent startup: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 21 {
		t.Fatalf("migration count = %d, want 21", count)
	}
}

func TestConcurrentMigrationBodyAppliedOnce(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	if err := initMigrationTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	const body = `CREATE TABLE exactly_once(value INTEGER NOT NULL); INSERT INTO exactly_once VALUES (7)`
	const workers = 8
	var wg sync.WaitGroup
	errors := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- applyOne(ctx, db, "test_once", body) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM exactly_once`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("body repeated: %d, %v", count, err)
	}
	// A failed DDL transaction must release its lock and preserve retryability.
	if err := applyOne(ctx, db, "test_retry", `CREATE TABLE rolled_back(v INTEGER); SELECT 1/0`); err == nil {
		t.Fatal("expected failure")
	}
	if err := applyOne(ctx, db, "test_retry", `CREATE TABLE rolled_back(v INTEGER)`); err != nil {
		t.Fatal(err)
	}
}
