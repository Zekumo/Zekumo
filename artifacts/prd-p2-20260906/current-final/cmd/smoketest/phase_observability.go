package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"
)

func phaseObservability(s *state) {
	// Stats: async recorders need a beat to land in Redis.
	time.Sleep(700 * time.Millisecond)
	var st struct {
		Days []struct {
			Active     int `json:"active"`
			Logins     int `json:"logins"`
			NewPlayers int `json:"new_players"`
		} `json:"days"`
	}
	step("game stats", call("GET", "/admin/api/games/"+s.gameID+"/stats?days=7", s.adminToken, nil, &st))
	today := st.Days[len(st.Days)-1]
	step("stats counted activity", boolErr(today.Active >= 2 && today.Logins >= 3 && today.NewPlayers >= 2,
		"today = %+v", today))

	// Cloud function outbound HTTP: allowlist gate + fetch roundtrip.
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok": true, "source": "external-api"}`)
	}))
	defer apiSrv.Close()

	step("save fetch function", call("PUT", s.fnBase+"/fetcher", s.adminToken, map[string]any{
		"code": "mc.http.fetch(request.body.url)", "enabled": true, "public": false, "cron_secs": 0}, nil))
	step("fetch blocked before allowlist", expectErr(call("POST", "/v1/functions/fetcher", s.alice.Token,
		map[string]any{"url": apiSrv.URL}, nil)))
	step("set http allowlist", call("PUT", "/admin/api/games/"+s.gameID+"/http-allowlist", s.adminToken,
		map[string]string{"hosts": "127.0.0.1"}, nil))
	var fetchRes struct {
		Result struct {
			Status int            `json:"status"`
			JSON   map[string]any `json:"json"`
		} `json:"result"`
	}
	step("fetch via allowlist", call("POST", "/v1/functions/fetcher", s.alice.Token,
		map[string]any{"url": apiSrv.URL}, &fetchRes))
	step("fetch response parsed", boolErr(fetchRes.Result.Status == 200 && fetchRes.Result.JSON["ok"] == true,
		"got %+v", fetchRes.Result))
	step("non-whitelisted host still blocked", expectErr(call("POST", "/v1/functions/fetcher", s.alice.Token,
		map[string]any{"url": "http://not-allowed.example/x"}, nil)))

	// Structured logs: client report, http access attribution, function errors.
	step("client reports log", call("POST", "/v1/logs", s.alice.Token, map[string]any{
		"level": "error", "event": "crash", "message": "boom at level3",
		"fields": map[string]any{"scene": "level3"}}, nil))
	time.Sleep(1500 * time.Millisecond) // batch writer flushes every second

	var logRes struct {
		Entries []struct {
			Message string         `json:"message"`
			Fields  map[string]any `json:"fields"`
		} `json:"entries"`
	}
	step("query client logs", call("GET",
		"/admin/api/logs?game_id="+s.gameID+"&source=client&q=boom", s.adminToken, nil, &logRes))
	step("client log recorded with player", boolErr(
		len(logRes.Entries) >= 1 && logRes.Entries[0].Fields["player_id"] == s.alice.Player.ID,
		"got %+v", logRes.Entries))

	var httpLogs struct {
		Entries []struct{} `json:"entries"`
	}
	// Successful fast requests are intentionally not logged; the failed
	// allowlist calls above are what should show up.
	step("query http access logs", call("GET",
		"/admin/api/logs?game_id="+s.gameID+"&source=http&level=warn", s.adminToken, nil, &httpLogs))
	step("failed requests logged and attributed", boolErr(len(httpLogs.Entries) >= 1, "no game-scoped http logs"))

	var fnLogs struct {
		Entries []struct {
			Event string `json:"event"`
		} `json:"entries"`
	}
	step("query function error logs", call("GET",
		"/admin/api/logs?game_id="+s.gameID+"&source=funcs&level=error", s.adminToken, nil, &fnLogs))
	step("function error logged", boolErr(len(fnLogs.Entries) >= 1 && fnLogs.Entries[0].Event == "fetcher",
		"got %+v", fnLogs.Entries))
}
