package main

func phaseFunctions(s *state) {
	// Cloud functions: shared KV, caller identity, public invocation, leaderboard access.
	step("save function counter", call("PUT", s.fnBase+"/counter", s.adminToken, map[string]any{
		"code":    "const n = (mc.kv.get('c') || 0) + 1; mc.kv.set('c', n); ({n: n, who: request.player ? request.player.nickname : null})",
		"enabled": true, "public": false, "cron_secs": 0}, nil))
	var fnRes struct {
		Result map[string]any `json:"result"`
	}
	step("admin test-run function", call("POST", s.fnBase+"/counter/test", s.adminToken, map[string]any{}, &fnRes))
	step("function state persisted (n=1)", boolErr(fnRes.Result["n"] == float64(1), "got %v", fnRes.Result))
	step("player calls function", call("POST", "/v1/functions/counter", s.alice.Token, map[string]any{}, &fnRes))
	step("function sees caller (n=2)", boolErr(fnRes.Result["n"] == float64(2) && fnRes.Result["who"] == s.alice.Player.Nickname,
		"got %v", fnRes.Result))

	step("save public function", call("PUT", s.fnBase+"/hello", s.adminToken, map[string]any{
		"code": "({msg: 'hi', from: request.method})", "enabled": true, "public": true, "cron_secs": 0}, nil))
	step("public function without token", call("GET", "/v1/apps/"+s.appID+"/functions/hello", "", nil, &fnRes))
	step("private function hidden from public url", expectErr(call("GET", "/v1/apps/"+s.appID+"/functions/counter", "", nil, nil)))

	step("save leaderboard function", call("PUT", s.fnBase+"/ranktop", s.adminToken, map[string]any{
		"code": "mc.leaderboard.top('weekly', 5)", "enabled": true, "public": false, "cron_secs": 0}, nil))
	var fnArr struct {
		Result []any `json:"result"`
	}
	step("function reads leaderboard", call("POST", "/v1/functions/ranktop", s.alice.Token, map[string]any{}, &fnArr))
	step("leaderboard rows from function", boolErr(len(fnArr.Result) == 2, "got %d rows", len(fnArr.Result)))
}
