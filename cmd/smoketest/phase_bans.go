package main

import "fmt"

// phaseBans exercises player bans. The property that matters is immediacy: a
// ban has to kill the token the player already holds, not just stop the next
// login — otherwise a cheater keeps playing until their session expires. That
// works because the ban is written to a Redis blacklist the auth middleware
// checks on every request.
//
// This phase runs last: it bans alice, and every other phase needs her token.
func phaseBans(s *state) {
	fmt.Println("\n-- bans --")

	// No bans on a fresh game.
	var empty struct {
		Bans []any `json:"bans"`
	}
	step("ban list empty initially", call("GET", "/admin/api/games/"+s.gameID+"/bans", s.adminToken, nil, &empty))
	step("no bans yet", boolErr(len(empty.Bans) == 0, "got %d, want 0", len(empty.Bans)))

	// Alice's token works right now — the baseline the ban has to change.
	step("alice's token works before the ban", call("GET", "/v1/player/profile", s.alice.Token, nil, nil))

	// A reason is mandatory: a ban with no recorded reason is unappealable.
	step("ban without reason rejected", expectErr(call("POST", "/admin/api/players/"+s.alice.Player.ID+"/ban",
		s.adminToken, map[string]any{}, nil)))
	step("negative duration rejected", expectErr(call("POST", "/admin/api/players/"+s.alice.Player.ID+"/ban",
		s.adminToken, map[string]any{"reason": "测试", "duration_secs": -1}, nil)))
	step("banning an unknown player is 404", expectErr(call("POST",
		"/admin/api/players/00000000-0000-0000-0000-000000000000/ban",
		s.adminToken, map[string]any{"reason": "测试"}, nil)))

	// Ban bob permanently (duration 0), and alice temporarily, so both the
	// TTL and the no-TTL path are covered.
	var bobBan struct {
		ID        string  `json:"id"`
		PlayerID  string  `json:"player_id"`
		Reason    string  `json:"reason"`
		Operator  string  `json:"operator"`
		ExpiresAt *string `json:"expires_at"`
	}
	step("admin bans bob permanently", call("POST", "/admin/api/players/"+s.bob.Player.ID+"/ban", s.adminToken,
		map[string]any{"reason": "使用外挂"}, &bobBan))
	step("permanent ban has no expiry", boolErr(bobBan.ExpiresAt == nil && bobBan.Reason == "使用外挂",
		"ban = %+v, want reason 使用外挂 and no expiry", bobBan))
	step("ban records the operator", boolErr(bobBan.Operator != "", "operator is empty; the audit trail needs it"))

	// The already-issued token must stop working immediately, with a code the
	// client can branch on to show a ban notice rather than a login screen.
	step("bob's existing token is dead", expectErr(call("GET", "/v1/player/profile", s.bob.Token, nil, nil)))
	step("bob cannot use any player API", expectErr(call("GET", "/v1/currency", s.bob.Token, nil, nil)))
	step("bob cannot log in again", expectErr(call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "password", "username": "bob123", "password": "secret66"}, nil)))

	// A second ban on the same player is a conflict, not a silent overwrite —
	// two active bans would make the unban ambiguous.
	step("double ban rejected", expectErr(call("POST", "/admin/api/players/"+s.bob.Player.ID+"/ban", s.adminToken,
		map[string]any{"reason": "再来一次"}, nil)))

	// Timed ban on alice: the expiry is recorded, and the Redis key carries
	// the same TTL so the ban lifts itself.
	var aliceBan struct {
		ExpiresAt *string `json:"expires_at"`
	}
	step("admin bans alice for 1 hour", call("POST", "/admin/api/players/"+s.alice.Player.ID+"/ban", s.adminToken,
		map[string]any{"reason": "刷分", "duration_secs": 3600}, &aliceBan))
	step("timed ban records an expiry", boolErr(aliceBan.ExpiresAt != nil,
		"expires_at is nil; a timed ban must say when it ends"))
	step("alice's token is dead too", expectErr(call("GET", "/v1/player/profile", s.alice.Token, nil, nil)))

	// Both bans show in the audit list, with the nickname joined in so an
	// operator does not have to look each id up.
	var list struct {
		Bans []struct {
			PlayerID string  `json:"player_id"`
			Reason   string  `json:"reason"`
			Nickname string  `json:"nickname"`
			LiftedAt *string `json:"lifted_at"`
		} `json:"bans"`
	}
	step("admin lists bans", call("GET", "/admin/api/games/"+s.gameID+"/bans", s.adminToken, nil, &list))
	step("two active bans listed", boolErr(len(list.Bans) == 2, "got %d, want 2", len(list.Bans)))
	named := len(list.Bans) == 2
	for _, b := range list.Bans {
		if b.Nickname == "" || b.LiftedAt != nil {
			named = false
		}
	}
	step("bans carry nicknames and are unlifted", boolErr(named, "bans = %+v", list.Bans))

	// Unban bob: the blacklist entry goes away, so the next login works again.
	step("admin unbans bob", call("POST", "/admin/api/players/"+s.bob.Player.ID+"/unban", s.adminToken, nil, nil))
	step("unbanning twice is 404", expectErr(call("POST", "/admin/api/players/"+s.bob.Player.ID+"/unban",
		s.adminToken, nil, nil)))

	// Unban writes Postgres and clears the Redis key before it replies, so the
	// next login works with no wait — no polling needed here.
	var bobAgain loginRes
	step("bob can log in after the unban", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "password", "username": "bob123", "password": "secret66"}, &bobAgain))
	step("the fresh token works", call("GET", "/v1/player/profile", bobAgain.Token, nil, nil))
	s.bob = bobAgain // cleanup and any later phase need a live token

	// The lifted ban stays in the list as history, now marked lifted — a ban
	// record that vanished would destroy the audit trail.
	var afterUnban struct {
		Bans []struct {
			PlayerID string  `json:"player_id"`
			LiftedAt *string `json:"lifted_at"`
			LiftedBy string  `json:"lifted_by"`
		} `json:"bans"`
	}
	step("ban history survives the unban", call("GET", "/admin/api/games/"+s.gameID+"/bans", s.adminToken, nil, &afterUnban))
	step("both bans still on record", boolErr(len(afterUnban.Bans) == 2, "got %d, want 2", len(afterUnban.Bans)))
	var bobRecord struct {
		found  bool
		lifted bool
		by     string
	}
	for _, b := range afterUnban.Bans {
		if b.PlayerID == s.bob.Player.ID {
			bobRecord.found = true
			bobRecord.lifted = b.LiftedAt != nil
			bobRecord.by = b.LiftedBy
		}
	}
	step("bob's ban is marked lifted", boolErr(bobRecord.found && bobRecord.lifted && bobRecord.by != "",
		"record = %+v, want lifted with an operator", bobRecord))

	// Alice stays banned, so cleanup runs against a banned player on purpose:
	// deleting the game must still work.
	step("alice is still banned", expectErr(call("GET", "/v1/player/profile", s.alice.Token, nil, nil)))
}
