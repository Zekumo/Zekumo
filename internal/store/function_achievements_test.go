package store

import (
	"testing"
	"zekumo/internal/achievements"
	"zekumo/internal/funcs"
	"zekumo/internal/repo"
)

type achievementEvents struct{ count int }

func (e *achievementEvents) Emit(gameID, event string, data any) {
	if event == "achievement.unlocked" {
		e.count++
	}
}

func TestCloudFunctionAchievementReward(t *testing.T) {
	db, ctx := isolatedPostgres(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	games := repo.Games{DB: db}
	g, err := games.Create(ctx, "achievement-functions")
	if err != nil {
		t.Fatal(err)
	}
	p, err := (repo.Players{DB: db}).Create(ctx, g.ID, "guest", "device", "", "Player")
	if err != nil {
		t.Fatal(err)
	}
	defs := repo.AchievementDefs{DB: db}
	_, err = defs.Create(ctx, g.ID, repo.AchievementDef{Key: "wins", Name: "Wins", Rarity: "common", Type: "progress", Target: 2})
	if err != nil {
		t.Fatal(err)
	}
	events := &achievementEvents{}
	rt := &funcs.Runtime{Achievements: &achievements.Service{Defs: defs, Unlocks: repo.AchievementUnlocks{DB: db}}, Events: events}
	fn := &repo.CloudFunction{Name: "reward", Code: `mc.achievements.progress(request.player.id,'wins',2); mc.achievements.unlock(request.player.id,'wins'); true`}
	result, err := rt.Execute(ctx, g.ID, fn, funcs.Request{Player: map[string]any{"id": p.ID}})
	if err != nil || result.Result != true || events.count != 1 {
		t.Fatalf("reward: %+v %v events=%d", result, err, events.count)
	}
	other, err := games.Create(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Execute(ctx, other.ID, fn, funcs.Request{Player: map[string]any{"id": p.ID}}); err == nil {
		t.Fatal("cross-game award accepted")
	}
}
