package main

import (
	"fmt"
	"net/http"
	"net/url"
)

func phaseSSO(s *state) {
	// SSO (platform accounts / 通行证).
	s.accountUsr = "carol_" + s.suffix
	var carol acctRes
	step("sso register account", call("POST", "/sso/api/register", "",
		map[string]string{"username": s.accountUsr, "password": "hunter22", "nickname": "卡罗尔"}, &carol))
	s.accountIDs = append(s.accountIDs, carol.Account.ID)
	var carol2 acctRes
	step("sso account login", call("POST", "/sso/api/login", "",
		map[string]string{"username": s.accountUsr, "password": "hunter22"}, &carol2))
	s.accountTok = carol2.Token

	var tick struct {
		Ticket string `json:"ticket"`
	}
	step("sso issue ticket", call("POST", "/sso/api/tickets", s.accountTok,
		map[string]string{"app_id": s.appID}, &tick))

	var carolPlayer loginRes
	step("game login via ticket", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "sso", "ticket": tick.Ticket}, &carolPlayer))
	step("ticket is one-time", expectErr(call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "sso", "ticket": tick.Ticket}, nil)))

	var carolPlayer2 loginRes
	step("game login via sso password", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "sso", "username": s.accountUsr, "password": "hunter22"}, &carolPlayer2))
	step("same player across sso logins", boolErr(carolPlayer.Player.ID == carolPlayer2.Player.ID,
		"ticket login player %s != password login player %s", carolPlayer.Player.ID, carolPlayer2.Player.ID))

	var meRes struct {
		Players []struct {
			PlayerID string `json:"player_id"`
		} `json:"players"`
	}
	step("sso me lists linked player", call("GET", "/sso/api/me", s.accountTok, nil, &meRes))
	step("linked player matches", boolErr(len(meRes.Players) == 1 && meRes.Players[0].PlayerID == carolPlayer.Player.ID,
		"unexpected linked players: %+v", meRes.Players))

	// Binding an existing guest player to a fresh account keeps the player.
	var dave acctRes
	step("sso register second account", call("POST", "/sso/api/register", "",
		map[string]string{"username": "dave_" + s.suffix, "password": "hunter33"}, &dave))
	s.accountIDs = append(s.accountIDs, dave.Account.ID)
	step("bind rejects taken account", expectErr(call("POST", "/v1/player/bind", s.alice.Token,
		map[string]string{"username": s.accountUsr, "password": "hunter22"}, nil)))
	step("bind guest to account", call("POST", "/v1/player/bind", s.alice.Token,
		map[string]string{"username": "dave_" + s.suffix, "password": "hunter33"}, nil))
	step("double bind rejected", expectErr(call("POST", "/v1/player/bind", s.alice.Token,
		map[string]string{"username": "dave_" + s.suffix, "password": "hunter33"}, nil)))
	var aliceViaSSO loginRes
	step("sso login resumes bound player", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "sso", "username": "dave_" + s.suffix, "password": "hunter33"}, &aliceViaSSO))
	step("bound player preserved", boolErr(aliceViaSSO.Player.ID == s.alice.Player.ID,
		"sso login player %s != original guest %s", aliceViaSSO.Player.ID, s.alice.Player.ID))

	// Hosted authorize page: whitelist enforcement.
	step("set sso redirect whitelist", call("PUT", "/admin/api/games/"+s.gameID+"/sso", s.adminToken,
		map[string]string{"redirect_urls": "https://game.example.com/cb"}, nil))
	var info struct {
		RedirectValid bool `json:"redirect_valid"`
	}
	step("authorize info allows whitelisted", call("GET",
		"/sso/api/authorize/info?app_id="+s.appID+"&redirect_uri="+url.QueryEscape("https://game.example.com/cb?x=1"), "", nil, &info))
	step("whitelisted uri accepted", boolErr(info.RedirectValid, "expected redirect_valid=true"))
	step("authorize info rejects others", call("GET",
		"/sso/api/authorize/info?app_id="+s.appID+"&redirect_uri="+url.QueryEscape("https://evil.example.com/cb"), "", nil, &info))
	step("non-whitelisted uri rejected", boolErr(!info.RedirectValid, "expected redirect_valid=false"))

	// A prefix-only check would hand the login ticket to these two hosts.
	for _, evil := range []string{
		"https://game.example.com.evil.com/cb",
		"https://game.example.com@evil.com/cb",
	} {
		step("redirect bypass rejected: "+evil, func() error {
			if err := call("GET", "/sso/api/authorize/info?app_id="+s.appID+
				"&redirect_uri="+url.QueryEscape(evil), "", nil, &info); err != nil {
				return err
			}
			return boolErr(!info.RedirectValid, "expected redirect_valid=false for %s", evil)
		}())
	}

	pageRes, err := http.Get(base + "/sso/authorize?app_id=" + s.appID)
	if err == nil {
		pageRes.Body.Close()
		if pageRes.StatusCode != 200 {
			err = fmt.Errorf("status %d", pageRes.StatusCode)
		}
	}
	step("authorize page served", err)

	step("admin list accounts", call("GET", "/admin/api/accounts?search="+s.suffix, s.adminToken, nil, nil))
}
