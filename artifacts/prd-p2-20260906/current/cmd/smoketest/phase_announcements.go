package main

import "fmt"

// phaseAnnouncements exercises the announcement publish / poll flow.
func phaseAnnouncements(s *state) {
	fmt.Println("\n-- announcements --")

	// Public list when empty — should return empty array, not error.
	var empty struct {
		Announcements []any `json:"announcements"`
	}
	step("public list empty initially", call("GET", "/v1/apps/"+s.appID+"/announcements", "", nil, &empty))

	// Admin creates two announcements.
	var ann1 struct {
		ID string `json:"id"`
	}
	step("admin create info announcement", call("POST", "/admin/api/games/"+s.gameID+"/announcements", s.adminToken,
		map[string]any{
			"title":      "服务器维护通知",
			"body":       "本周五 23:00 进行例行维护，预计持续 1 小时。",
			"importance": "warning",
		}, &ann1))

	var ann2 struct {
		ID string `json:"id"`
	}
	step("admin create critical announcement", call("POST", "/admin/api/games/"+s.gameID+"/announcements", s.adminToken,
		map[string]any{
			"title":      "新版本上线",
			"body":       "v1.1.0 已正式发布，带来全新关卡！",
			"importance": "info",
		}, &ann2))

	// Missing title should fail.
	step("missing title rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/announcements", s.adminToken,
		map[string]any{"body": "no title"}, nil)))

	// Public poll returns both.
	var list struct {
		Announcements []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Importance string `json:"importance"`
		} `json:"announcements"`
	}
	step("public list returns 2", call("GET", "/v1/apps/"+s.appID+"/announcements", "", nil, &list))
	step("two announcements visible", boolErr(len(list.Announcements) == 2,
		"expected 2, got %d", len(list.Announcements)))

	// Incremental poll: after_id = ann1.ID should return only ann2.
	var incremental struct {
		Announcements []struct{ ID string } `json:"announcements"`
	}
	step("incremental poll after ann1", call("GET",
		"/v1/apps/"+s.appID+"/announcements?after_id="+ann1.ID, "", nil, &incremental))
	step("incremental returns 1", boolErr(len(incremental.Announcements) == 1,
		"expected 1 after incremental, got %d", len(incremental.Announcements)))
	if len(incremental.Announcements) == 1 {
		step("incremental returns ann2", boolErr(incremental.Announcements[0].ID == ann2.ID,
			"expected ann2 id %s, got %s", ann2.ID, incremental.Announcements[0].ID))
	}

	// Admin list (including inactive).
	var adminList struct {
		Announcements []struct{ ID string } `json:"announcements"`
	}
	step("admin list returns 2", call("GET", "/admin/api/games/"+s.gameID+"/announcements", s.adminToken, nil, &adminList))
	step("admin sees 2", boolErr(len(adminList.Announcements) == 2,
		"expected 2 in admin list, got %d", len(adminList.Announcements)))

	// Admin update ann1.
	step("admin update announcement", call("PUT", "/admin/api/announcements/"+ann1.ID, s.adminToken,
		map[string]any{
			"title":      "服务器维护通知（更新）",
			"body":       "维护时间调整为周六 01:00。",
			"importance": "warning",
		}, nil))

	// Deactivate ann1 — should disappear from public list.
	step("admin deactivate ann1", call("DELETE", "/admin/api/announcements/"+ann1.ID, s.adminToken, nil, nil))
	var afterDeactivate struct {
		Announcements []struct{ ID string } `json:"announcements"`
	}
	step("public list after deactivate", call("GET", "/v1/apps/"+s.appID+"/announcements", "", nil, &afterDeactivate))
	step("only 1 active announcement", boolErr(len(afterDeactivate.Announcements) == 1,
		"expected 1 active, got %d", len(afterDeactivate.Announcements)))
	if len(afterDeactivate.Announcements) == 1 {
		step("ann2 still visible", boolErr(afterDeactivate.Announcements[0].ID == ann2.ID,
			"wrong announcement visible after deactivate"))
	}

	// Admin list still shows both (including inactive).
	var adminListAfter struct {
		Announcements []struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		} `json:"announcements"`
	}
	step("admin list shows both after deactivate", call("GET",
		"/admin/api/games/"+s.gameID+"/announcements", s.adminToken, nil, &adminListAfter))
	step("admin sees 2 total", boolErr(len(adminListAfter.Announcements) == 2,
		"expected 2 in admin list, got %d", len(adminListAfter.Announcements)))

	// Deactivate non-existent ID should 404.
	step("deactivate unknown id returns 404", expectErr(call("DELETE",
		"/admin/api/announcements/00000000-0000-0000-0000-000000000000", s.adminToken, nil, nil)))

	// Targeted notices are only returned to matching platform/channel clients.
	var targeted struct {
		ID string `json:"id"`
	}
	step("create targeted announcement", call("POST", "/admin/api/games/"+s.gameID+"/announcements", s.adminToken,
		map[string]any{"title": "Windows 活动", "platform": "windows", "channel": "beta"}, &targeted))
	var filtered struct {
		Announcements []struct{ ID string } `json:"announcements"`
	}
	step("targeted announcement hidden from other platform", call("GET", "/v1/apps/"+s.appID+"/announcements?platform=web&channel=stable", "", nil, &filtered))
	var foundTarget bool
	for _, a := range filtered.Announcements {
		if a.ID == targeted.ID {
			foundTarget = true
		}
	}
	step("targeted announcement not returned", boolErr(!foundTarget, "targeted notice leaked to non-matching client"))
}
