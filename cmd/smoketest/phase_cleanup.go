package main

// phaseCleanup inspects what the run produced, then removes the game — which
// cascades to every row the other phases created.
func phaseCleanup(s *state) {
	step("admin list players", call("GET", "/admin/api/games/"+s.gameID+"/players", s.adminToken, nil, nil))
	step("admin view player data", call("GET", "/admin/api/players/"+s.alice.Player.ID+"/data", s.adminToken, nil, nil))
	if s.oauthClientID != "" {
		step("admin delete oauth client",
			call("DELETE", "/admin/api/oauth/clients/"+s.oauthClientID, s.adminToken, nil, nil))
	}
	failures := 0
	for _, id := range s.accountIDs {
		if call("DELETE", "/admin/api/accounts/"+id, s.adminToken, nil, nil) != nil {
			failures++
		}
	}
	step("admin delete test accounts",
		boolErr(failures == 0, "%d of %d accounts were not removed", failures, len(s.accountIDs)))
	step("admin delete game", call("DELETE", "/admin/api/games/"+s.gameID, s.adminToken, nil, nil))
}
