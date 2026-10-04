package main

import "fmt"

// phaseAchievements exercises achievement CRUD and unlock flow.
func phaseAchievements(s *state) {
	fmt.Println("\n-- achievements --")

	// Admin creates two achievement definitions.
	var achInstant struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	step("admin create instant achievement", call("POST", "/admin/api/games/"+s.gameID+"/achievements", s.adminToken,
		map[string]any{
			"key":         "first_blood",
			"name":        "初见锋芒",
			"description": "赢得第一场战斗",
			"rarity":      "common",
			"type":        "instant",
			"target":      1,
		}, &achInstant))

	var achProgress struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	step("admin create progress achievement", call("POST", "/admin/api/games/"+s.gameID+"/achievements", s.adminToken,
		map[string]any{
			"key":         "veteran",
			"name":        "老兵",
			"description": "完成 10 场战斗",
			"rarity":      "rare",
			"type":        "progress",
			"target":      10,
		}, &achProgress))

	// Duplicate key should fail.
	step("duplicate key rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/achievements", s.adminToken,
		map[string]any{"key": "first_blood", "name": "重复"}, nil)))

	// Admin list returns both.
	var achList struct {
		Achievements []struct{ ID string } `json:"achievements"`
	}
	step("admin list achievements", call("GET", "/admin/api/games/"+s.gameID+"/achievements", s.adminToken, nil, &achList))
	step("two achievements created", boolErr(len(achList.Achievements) == 2,
		"expected 2, got %d", len(achList.Achievements)))
	var hidden struct {
		ID string `json:"id"`
	}
	step("admin create hidden achievement", call("POST", "/admin/api/games/"+s.gameID+"/achievements", s.adminToken,
		map[string]any{
			"key": "secret_room", "name": "密室", "description": "找到隐藏房间",
			"icon_url": "https://example.com/secret.png", "hidden": true, "type": "instant",
		}, &hidden))

	// Player list: neither unlocked yet.
	var playerList struct {
		Achievements []struct {
			ID          string `json:"id"`
			Key         string `json:"key"`
			Name        string `json:"name"`
			Description string `json:"description"`
			IconURL     string `json:"icon_url"`
			Unlocked    bool   `json:"unlocked"`
		} `json:"achievements"`
	}
	step("alice list achievements (none unlocked)", call("GET", "/v1/achievements", s.alice.Token, nil, &playerList))
	var anyUnlocked bool
	for _, a := range playerList.Achievements {
		if a.Unlocked {
			anyUnlocked = true
		}
	}
	step("none unlocked initially", boolErr(!anyUnlocked, "expected no unlocked achievements"))
	var hiddenRedacted bool
	for _, a := range playerList.Achievements {
		if a.ID == hidden.ID {
			hiddenRedacted = a.Name == "???" && a.Description == "" && a.IconURL == ""
		}
	}
	step("hidden achievement details redacted", boolErr(hiddenRedacted,
		"locked hidden achievement exposed its details"))

	// Admin unlocks instant achievement for Alice.
	step("admin unlock for alice", call("POST", "/admin/api/achievements/"+achInstant.ID+"/unlock", s.adminToken,
		map[string]string{"player_id": s.alice.Player.ID}, nil))

	// Idempotent: unlock again should not error.
	step("unlock idempotent", call("POST", "/admin/api/achievements/"+achInstant.ID+"/unlock", s.adminToken,
		map[string]string{"player_id": s.alice.Player.ID}, nil))

	// Alice sees it unlocked.
	var afterUnlock struct {
		Achievements []struct {
			Key      string `json:"key"`
			Unlocked bool   `json:"unlocked"`
		} `json:"achievements"`
	}
	step("alice list after unlock", call("GET", "/v1/achievements", s.alice.Token, nil, &afterUnlock))
	var firstBloodUnlocked bool
	for _, a := range afterUnlock.Achievements {
		if a.Key == "first_blood" && a.Unlocked {
			firstBloodUnlocked = true
		}
	}
	step("first_blood unlocked for alice", boolErr(firstBloodUnlocked, "first_blood not showing as unlocked"))

	// GET /v1/achievements/unlocked
	var unlocked struct {
		Unlocked []struct{ Key string } `json:"unlocked"`
	}
	step("alice unlocked list", call("GET", "/v1/achievements/unlocked", s.alice.Token, nil, &unlocked))
	step("one entry in unlocked list", boolErr(len(unlocked.Unlocked) == 1,
		"expected 1 unlocked, got %d", len(unlocked.Unlocked)))

	// Progress achievement: partial advance (5 of 10) — should not unlock yet.
	step("partial progress (5/10)", call("POST", "/admin/api/achievements/"+achProgress.ID+"/unlock", s.adminToken,
		map[string]any{"player_id": s.alice.Player.ID, "progress": 5}, nil))
	var progressList struct {
		Achievements []struct {
			Key      string `json:"key"`
			Unlocked bool   `json:"unlocked"`
			Progress int    `json:"progress"`
		} `json:"achievements"`
	}
	step("progress list after partial", call("GET", "/v1/achievements", s.alice.Token, nil, &progressList))
	var veteranEntry struct {
		Unlocked bool
		Progress int
	}
	for _, a := range progressList.Achievements {
		if a.Key == "veteran" {
			veteranEntry.Unlocked = a.Unlocked
			veteranEntry.Progress = a.Progress
		}
	}
	step("veteran not unlocked at 5/10", boolErr(!veteranEntry.Unlocked, "should not be unlocked yet"))
	step("veteran progress is 5", boolErr(veteranEntry.Progress == 5, "progress = %d, want 5", veteranEntry.Progress))

	// Lowering a definition target reconciles existing progress immediately.
	step("lower progress target to current progress", call("PUT", "/admin/api/achievements/"+achProgress.ID, s.adminToken,
		map[string]any{
			"name": "老兵", "description": "完成 5 场战斗", "rarity": "rare",
			"type": "progress", "target": 5,
		}, nil))
	var completedList struct {
		Unlocked []struct{ Key string } `json:"unlocked"`
	}
	step("alice unlocked after target edit", call("GET", "/v1/achievements/unlocked", s.alice.Token, nil, &completedList))
	step("two achievements unlocked", boolErr(len(completedList.Unlocked) == 2,
		"expected 2 unlocked, got %d", len(completedList.Unlocked)))

	// Definitions with an unlocked player are retained so the unlock history
	// cannot disappear accidentally from an operator delete.
	step("cannot delete unlocked achievement", expectErr(call("DELETE", "/admin/api/achievements/"+achInstant.ID, s.adminToken, nil, nil)))

	// Bob has no unlocks — list should be empty array, not null.
	var bobUnlocked struct {
		Unlocked []any `json:"unlocked"`
	}
	step("bob unlocked list is empty", call("GET", "/v1/achievements/unlocked", s.bob.Token, nil, &bobUnlocked))

	// Admin update and delete.
	step("admin update achievement", call("PUT", "/admin/api/achievements/"+achInstant.ID, s.adminToken,
		map[string]any{"name": "初见锋芒·改", "description": "更新描述", "rarity": "rare", "type": "instant", "target": 1}, nil))
}
