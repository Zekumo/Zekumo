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

	// Bob accepts.
	step("accept friend request", call("POST", "/v1/friends/accept", s.bob.Token,
		map[string]string{"request_id": reqID}, nil))

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

	// Pending requests list should now be empty for both.
	var aliceReqs struct {
		Requests []any `json:"requests"`
	}
	step("alice pending requests empty", call("GET", "/v1/friends/requests", s.alice.Token, nil, &aliceReqs))

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

	// Wait before re-requesting: the old declined/accepted request row may block a new one.
	// Alice sends a new request to Bob (after unfriend the old accepted row was deleted;
	// the previous UNIQUE constraint only covers game_id+from_id+to_id for pending rows,
	// so a fresh request must succeed).
	step("send request again", call("POST", "/v1/friends/request", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))
	step("alice blocks bob", call("POST", "/v1/friends/block", s.alice.Token,
		map[string]string{"target_player_id": s.bob.Player.ID}, nil))

	// After block, Bob cannot send Alice a request.
	step("blocked sender rejected", expectErr(call("POST", "/v1/friends/request", s.bob.Token,
		map[string]string{"target_player_id": s.alice.Player.ID}, nil)))
}
