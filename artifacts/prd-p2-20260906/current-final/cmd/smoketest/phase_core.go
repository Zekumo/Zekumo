package main

import (
	"fmt"
	"time"
)

func phaseCore(s *state) {
	// Admin: login, create a game.
	var adminRes struct{ Token string }
	step("admin login", call("POST", "/admin/api/login", "",
		map[string]string{"username": env("ADMIN_USERNAME", "admin"), "password": env("ADMIN_PASSWORD", "admin123")}, &adminRes))
	s.adminToken = adminRes.Token

	var game struct {
		ID    string `json:"id"`
		AppID string `json:"app_id"`
	}
	step("admin create game", call("POST", "/admin/api/games", s.adminToken,
		map[string]string{"name": "冒烟测试游戏"}, &game))
	s.gameID, s.appID = game.ID, game.AppID
	s.fnBase = "/admin/api/games/" + s.gameID + "/functions"
	s.suffix = fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)

	// Guest login and password register/login.
	var alice, bob, bob2 loginRes
	step("guest login", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "guest", "device_id": "device-alice-001", "nickname": "爱丽丝"}, &alice))
	step("password register", call("POST", "/v1/auth/register", "",
		map[string]string{"app_id": s.appID, "username": "bob123", "password": "secret66", "nickname": "鲍勃"}, &bob))
	step("password login", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "password", "username": "bob123", "password": "secret66"}, &bob2))
	step("wrong password rejected", expectErr(call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "password", "username": "bob123", "password": "wrong"}, nil)))
	s.alice, s.bob = alice, bob

	// Profile + save data.
	step("update profile", call("PUT", "/v1/player/profile", s.alice.Token,
		map[string]any{"profile": map[string]any{"level": 5, "title": "见习冒险者"}}, nil))
	step("put save data", call("PUT", "/v1/player/data/save1", s.alice.Token,
		map[string]any{"gold": 120, "inventory": []string{"sword", "potion"}}, nil))
	var entry struct {
		Value map[string]any `json:"value"`
	}
	step("get save data", call("GET", "/v1/player/data/save1", s.alice.Token, nil, &entry))
	if entry.Value["gold"] != float64(120) {
		step("save data roundtrip", fmt.Errorf("gold = %v, want 120", entry.Value["gold"]))
	} else {
		step("save data roundtrip", nil)
	}
	step("list save keys", call("GET", "/v1/player/data", s.alice.Token, nil, nil))

	// Leaderboard.
	step("alice submit score", call("POST", "/v1/leaderboards/weekly/score", s.alice.Token, map[string]any{"score": 300}, nil))
	step("bob submit score", call("POST", "/v1/leaderboards/weekly/score", s.bob.Token, map[string]any{"score": 500}, nil))
	step("lower score ignored (max mode)", call("POST", "/v1/leaderboards/weekly/score", s.alice.Token, map[string]any{"score": 100}, nil))
	var top struct {
		Entries []struct {
			Rank     int    `json:"rank"`
			Nickname string `json:"nickname"`
			Score    int64  `json:"score"`
		} `json:"entries"`
	}
	step("get top", call("GET", "/v1/leaderboards/weekly", s.alice.Token, nil, &top))
	if len(top.Entries) != 2 || top.Entries[0].Score != 500 || top.Entries[1].Score != 300 {
		step("ranking order", fmt.Errorf("unexpected entries: %+v", top.Entries))
	} else {
		step("ranking order", nil)
	}
	var me struct {
		Rank int `json:"rank"`
	}
	step("get my rank", call("GET", "/v1/leaderboards/weekly/me", s.alice.Token, nil, &me))
	if me.Rank != 2 {
		step("my rank = 2", fmt.Errorf("rank = %d", me.Rank))
	} else {
		step("my rank = 2", nil)
	}

	// Dialogue scripts: admin writes, client reads.
	step("admin save dialogue", call("PUT", "/admin/api/games/"+s.gameID+"/dialogues/chapter1.intro", s.adminToken,
		map[string]any{"title": "第一章·开场", "content": []map[string]string{
			{"speaker": "艾拉", "text": "你终于来了。"},
			{"speaker": "旅人", "text": "路上遇到了点麻烦。"},
		}}, nil))
	var script struct {
		Content []struct {
			Speaker string `json:"speaker"`
		} `json:"content"`
		Version int `json:"version"`
	}
	step("client fetch dialogue", call("GET", "/v1/dialogues/chapter1.intro", s.alice.Token, nil, &script))
	if len(script.Content) != 2 || script.Content[0].Speaker != "艾拉" {
		step("dialogue content roundtrip", fmt.Errorf("unexpected content: %+v", script.Content))
	} else {
		step("dialogue content roundtrip", nil)
	}
	step("client list dialogues", call("GET", "/v1/dialogues", s.alice.Token, nil, nil))
}
