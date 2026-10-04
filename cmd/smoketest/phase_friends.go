package main

import "fmt"

// phaseFriends exercises the friend system: send request, accept, list, remove, block.
func phaseFriends(s *state) {
	fmt.Println("\n-- friends --")

	// Alice sends a friend request to Bob.
	step("send friend request", call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))

	// Duplicate request should return 409.
	step("duplicate request rejected", expectErr(call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil)))

	// Self-request should be rejected.
	step("self-request rejected", expectErr(call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.alice.Player.ID}, nil)))

	// Bob sees the incoming request.
	var reqs struct {
		Requests []struct {
			ID     string `json:"id"`
			FromID string `json:"from_id"`
			Status string `json:"status"`
		} `json:"requests"`
	}
	step("bob list requests", call("GET", "/v1/friends/requests", s.bob.Token, nil, &reqs))
	var reqID string
	for _, r := range reqs.Requests {
		if r.FromID == s.alice.Player.ID {
			reqID = r.ID
		}
	}
	step("request visible to bob", boolErr(reqID != "", "pending request from alice not found"))

	// A declined request can be sent again in the same direction.
	step("bob declines request", call("POST", "/v1/friends/decline", s.bob.Token,
		map[string]string{"request_id": reqID}, nil))
	step("retry after decline", call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))
	reqs.Requests = nil
	step("bob list retried request", call("GET", "/v1/friends/requests", s.bob.Token, nil, &reqs))
	reqID = ""
	for _, r := range reqs.Requests {
		if r.FromID == s.alice.Player.ID {
			reqID = r.ID
		}
	}
	step("retried request visible", boolErr(reqID != "", "retried request from alice not found"))
	step("reverse pending request rejected", expectErr(call("POST", "/v1/friends/request", s.bob.Token,
		map[string]string{"target_player_id": s.alice.Player.ID}, nil)))

	// Bob accepts.
	step("accept friend request", call("POST", "/v1/friends/accept", s.bob.Token,
		map[string]string{"request_id": reqID}, nil))
	step("repeated accept is a conflict", expectErr(call("POST", "/v1/friends/accept", s.bob.Token,
		map[string]string{"request_id": reqID}, nil)))

	// Alice's friend list now contains Bob.
	var friends struct {
		Friends []struct {
			PlayerID string `json:"player_id"`
			Nickname string `json:"nickname"`
		} `json:"friends"`
	}
	step("alice friend list", call("GET", "/v1/friends", s.alice.Token, nil, &friends))
	var hasBob bool
	for _, f := range friends.Friends {
		if f.PlayerID == s.bob.Player.ID {
			hasBob = true
		}
	}
	step("bob in alice's list", boolErr(hasBob, "bob not found in alice's friend list"))

	// Bob's friend list contains Alice.
	step("bob friend list", call("GET", "/v1/friends", s.bob.Token, nil, nil))
	var friendBoard struct {
		Entries []struct {
			PlayerID string `json:"player_id"`
		} `json:"entries"`
	}
	step("friends leaderboard scope", call("GET", "/v1/leaderboards/weekly?scope=friends", s.alice.Token, nil, &friendBoard))
	var leaked bool
	for _, entry := range friendBoard.Entries {
		if entry.PlayerID != s.alice.Player.ID && entry.PlayerID != s.bob.Player.ID {
			leaked = true
		}
	}
	step("friends leaderboard contains no outsiders", boolErr(!leaked, "friends scope returned an unrelated player"))
	step("friends leaderboard contains pair", boolErr(len(friendBoard.Entries) == 2,
		"expected alice and bob, got %d entries", len(friendBoard.Entries)))
	if len(friendBoard.Entries) == 2 {
		step("friends leaderboard preserves score order", boolErr(
			friendBoard.Entries[0].PlayerID == s.bob.Player.ID && friendBoard.Entries[1].PlayerID == s.alice.Player.ID,
			"friend leaderboard did not rank bob before alice"))
	}
	var friendPage struct {
		Entries []struct {
			Rank     int64  `json:"rank"`
			PlayerID string `json:"player_id"`
		} `json:"entries"`
	}
	step("friends leaderboard pagination", call("GET", "/v1/leaderboards/weekly?scope=friends&offset=1&limit=1", s.alice.Token, nil, &friendPage))
	step("friends leaderboard page rank", boolErr(len(friendPage.Entries) == 1 &&
		friendPage.Entries[0].PlayerID == s.alice.Player.ID && friendPage.Entries[0].Rank == 2,
		"unexpected friend page: %+v", friendPage.Entries))

	// Pending requests list should now be empty for both.
	var aliceReqs struct {
		Requests []any `json:"requests"`
	}
	step("alice pending requests empty", call("GET", "/v1/friends/requests", s.alice.Token, nil, &aliceReqs))
	step("no stale requests after accept", boolErr(len(aliceReqs.Requests) == 0,
		"expected no pending requests after accept, got %d", len(aliceReqs.Requests)))

	// Alice unfriends Bob.
	step("unfriend bob", call("DELETE", "/v1/friends/"+s.bob.Player.ID, s.alice.Token, nil, nil))
	var afterRemove struct {
		Friends []struct{ PlayerID string } `json:"friends"`
	}
	step("friend list after remove", call("GET", "/v1/friends", s.alice.Token, nil, &afterRemove))
	var stillHasBob bool
	for _, f := range afterRemove.Friends {
		if f.PlayerID == s.bob.Player.ID {
			stillHasBob = true
		}
	}
	step("bob removed from list", boolErr(!stillHasBob, "bob still in alice's friend list after remove"))

	// A sender may withdraw and retry a request.
	step("send request again", call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))
	var outgoing struct {
		Requests []struct {
			ID     string `json:"id"`
			FromID string `json:"from_id"`
		} `json:"requests"`
	}
	step("alice sees outgoing request", call("GET", "/v1/friends/requests", s.alice.Token, nil, &outgoing))
	var outgoingID string
	for _, r := range outgoing.Requests {
		if r.FromID == s.alice.Player.ID {
			outgoingID = r.ID
		}
	}
	step("outgoing request found", boolErr(outgoingID != "", "alice outgoing request not found"))
	step("alice withdraws request", call("POST", "/v1/friends/decline", s.alice.Token,
		map[string]string{"request_id": outgoingID}, nil))
	step("retry after withdrawal", call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))
	step("alice blocks bob", call("POST", "/v1/friends/block", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))

	// After block, Bob cannot send Alice a request.
	step("blocked sender rejected", expectErr(call("POST", "/v1/friends/request", s.bob.Token,
		map[string]string{"target_player_id": s.alice.Player.ID}, nil)))
}
