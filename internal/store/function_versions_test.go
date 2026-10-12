package store

import (
	"errors"
	"testing"
	"zekumo/internal/repo"
)

func TestFunctionVersionLifecycle(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	g, err := (repo.Games{DB: db}).Create(ctx, "versions")
	if err != nil {
		t.Fatal(err)
	}
	r := repo.Functions{DB: db}
	zero := int64(0)
	first, err := r.Save(ctx, g.ID, "hello", "1", true, false, 0, &zero)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Save(ctx, g.ID, "hello", "2", true, false, 0, &zero); !errors.Is(err, repo.ErrFunctionConflict) {
		t.Fatalf("create conflict: %v", err)
	}
	second, err := r.Save(ctx, g.ID, "hello", "2", true, true, 10, &first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Save(ctx, g.ID, "hello", "3", true, false, 0, &first.Revision); !errors.Is(err, repo.ErrFunctionConflict) {
		t.Fatalf("stale conflict: %v", err)
	}
	restored, err := r.Restore(ctx, g.ID, "hello", first.Revision, second.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Code != "1" || restored.Public || restored.CronSecs != 0 || restored.Revision <= second.Revision {
		t.Fatalf("restore: %+v", restored)
	}
	if _, err = r.Version(ctx, "00000000-0000-0000-0000-000000000099", "hello", first.Revision); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("tenant isolation: %v", err)
	}
	for i := 0; i < 52; i++ {
		if _, err = r.Upsert(ctx, g.ID, "hello", "4", true, false, 0); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := r.Versions(ctx, g.ID, "hello")
	if err != nil || len(versions) != 50 {
		t.Fatalf("retention: %d %v", len(versions), err)
	}
	if err = r.Delete(ctx, g.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Version(ctx, g.ID, "hello", restored.Revision); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
}

func TestFunctionDraftPublishLifecycle(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	g, err := (repo.Games{DB: db}).Create(ctx, "drafts")
	if err != nil {
		t.Fatal(err)
	}
	r := repo.Functions{DB: db}
	live, err := r.Upsert(ctx, g.ID, "hello", "1", true, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := r.SaveDraft(ctx, g.ID, "hello", repo.FunctionDraft{Code: "2", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	current, err := r.Get(ctx, g.ID, "hello")
	if err != nil || current.Code != "1" {
		t.Fatalf("draft changed live: %+v %v", current, err)
	}
	if _, err = r.SaveDraft(ctx, g.ID, "hello", repo.FunctionDraft{Code: "3"}, 0); !errors.Is(err, repo.ErrFunctionConflict) {
		t.Fatalf("draft create conflict: %v", err)
	}
	if _, err = r.PublishDraft(ctx, g.ID, "hello", live.Revision, draft.Revision+1); !errors.Is(err, repo.ErrFunctionConflict) {
		t.Fatalf("publish conflict: %v", err)
	}
	published, err := r.PublishDraft(ctx, g.ID, "hello", live.Revision, draft.Revision)
	if err != nil || published.Code != "2" {
		t.Fatalf("publish: %+v %v", published, err)
	}
	if _, err = r.Draft(ctx, g.ID, "hello"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("draft still present: %v", err)
	}
	if _, err = r.SaveDraft(ctx, g.ID, "hello", repo.FunctionDraft{Code: "stale"}, draft.Revision); !errors.Is(err, repo.ErrFunctionConflict) {
		t.Fatalf("stale update recreated draft: %v", err)
	}
	versions, err := r.Versions(ctx, g.ID, "hello")
	if err != nil || len(versions) != 2 {
		t.Fatalf("publication history: %v %v", versions, err)
	}
}

func TestFunctionScheduleClaimLifecycle(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	g, err := (repo.Games{DB: db}).Create(ctx, "schedules")
	if err != nil {
		t.Fatal(err)
	}
	r := repo.Functions{DB: db}
	f, err := r.Upsert(ctx, g.ID, "cron", "1", true, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := r.Schedule(ctx, g.ID, "cron")
	if err != nil || schedule.DispatchCount != 0 {
		t.Fatalf("initial schedule: %+v %v", schedule, err)
	}
	if _, err = db.Exec(ctx, `UPDATE cloud_function_schedules SET next_run_at=now()-interval '1 second' WHERE function_id=$1`, f.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := r.ClaimCron(ctx, 32)
	if err != nil || len(claimed) != 1 || claimed[0].Revision != f.Revision {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	again, err := r.ClaimCron(ctx, 32)
	if err != nil || len(again) != 0 {
		t.Fatalf("duplicate claim: %+v %v", again, err)
	}
	current, err := r.Get(ctx, g.ID, "cron")
	if err != nil || current.Revision != f.Revision {
		t.Fatal("claim modified live revision")
	}
	schedule, err = r.Schedule(ctx, g.ID, "cron")
	if err != nil || schedule.DispatchCount != 1 || schedule.LastDispatchedAt == nil {
		t.Fatalf("dispatch metadata: %+v %v", schedule, err)
	}
	if _, err = r.Upsert(ctx, g.ID, "cron", "2", false, false, 10); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM cloud_function_schedules WHERE function_id=$1`, f.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled schedule retained: %d %v", count, err)
	}
}
