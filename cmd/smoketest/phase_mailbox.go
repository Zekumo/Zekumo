package main

import "fmt"

// phaseMailbox exercises in-game mail: the admin sends targeted and broadcast
// mail, the player reads it, and claims its currency attachments. The claim is
// the interesting part — it moves money, so it must be one-shot and atomic:
// claiming twice must pay out once, and the payout must land in the wallet
// ledger phaseCurrency already verified.
func phaseMailbox(s *state) {
	fmt.Println("\n-- mailbox --")

	// Alice's inbox starts empty, and must answer with an empty array rather
	// than null so a client can iterate it without a nil check.
	var empty struct {
		Mail   []any `json:"mail"`
		Unread int   `json:"unread"`
	}
	step("inbox empty initially", call("GET", "/v1/mailbox", s.alice.Token, nil, &empty))
	step("no unread initially", boolErr(len(empty.Mail) == 0 && empty.Unread == 0,
		"got %d mail unread=%d, want 0 and 0", len(empty.Mail), empty.Unread))

	// Targeted mail with a 500 gold attachment, addressed to alice only.
	var sent struct {
		Mail struct {
			ID string `json:"id"`
		} `json:"mail"`
		Delivered int `json:"delivered"`
	}
	step("admin sends targeted mail with rewards", call("POST", "/admin/api/games/"+s.gameID+"/mail", s.adminToken,
		map[string]any{
			"title":      "新手礼包",
			"body":       "欢迎来到冒险世界，这是给你的见面礼。",
			"recipients": []string{s.alice.Player.ID},
			"rewards":    []map[string]any{{"currency_id": s.currencyID, "amount": 500}},
		}, &sent))
	s.mailID = sent.Mail.ID
	step("delivered to one recipient", boolErr(sent.Delivered == 1, "delivered = %d, want 1", sent.Delivered))

	// Broadcast mail reaches every player without naming anyone, which is how
	// a maintenance compensation goes out.
	var broadcast struct {
		Mail struct {
			ID string `json:"id"`
		} `json:"mail"`
	}
	step("admin sends broadcast mail", call("POST", "/admin/api/games/"+s.gameID+"/mail", s.adminToken,
		map[string]any{"title": "维护补偿", "body": "感谢等待。", "recipients": "all"}, &broadcast))

	// Validation: a reward in an unknown currency would create money that no
	// definition backs, so it is refused at send time, not at claim time.
	step("unknown reward currency rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/mail", s.adminToken,
		map[string]any{"title": "坏奖励", "recipients": "all",
			"rewards": []map[string]any{{"currency_id": "00000000-0000-0000-0000-000000000000", "amount": 1}}}, nil)))
	step("negative reward amount rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/mail", s.adminToken,
		map[string]any{"title": "负奖励", "recipients": "all",
			"rewards": []map[string]any{{"currency_id": s.currencyID, "amount": -5}}}, nil)))
	step("missing title rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/mail", s.adminToken,
		map[string]any{"recipients": "all"}, nil)))
	step("empty recipients rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/mail", s.adminToken,
		map[string]any{"title": "无人收件", "recipients": []string{}}, nil)))

	// Alice now sees both the targeted and the broadcast mail, both unread.
	var inbox struct {
		Mail []struct {
			ID      string  `json:"id"`
			Title   string  `json:"title"`
			ReadAt  *string `json:"read_at"`
			Rewards []struct {
				CurrencyID string `json:"currency_id"`
				Amount     int64  `json:"amount"`
			} `json:"rewards"`
		} `json:"mail"`
		Unread int `json:"unread"`
	}
	step("alice reads inbox", call("GET", "/v1/mailbox", s.alice.Token, nil, &inbox))
	step("two mail, both unread", boolErr(len(inbox.Mail) == 2 && inbox.Unread == 2,
		"got %d mail unread=%d, want 2 and 2", len(inbox.Mail), inbox.Unread))

	// Mark the targeted mail read; the unread count must drop by one.
	step("alice marks mail read", call("POST", "/v1/mailbox/"+s.mailID+"/read", s.alice.Token, nil, nil))
	var afterRead struct {
		Unread int `json:"unread"`
	}
	step("inbox after read", call("GET", "/v1/mailbox", s.alice.Token, nil, &afterRead))
	step("unread dropped to 1", boolErr(afterRead.Unread == 1, "unread = %d, want 1", afterRead.Unread))

	// unread_only filters out what was already read.
	var unreadOnly struct {
		Mail []struct {
			ID string `json:"id"`
		} `json:"mail"`
	}
	step("unread_only filter", call("GET", "/v1/mailbox?unread_only=true", s.alice.Token, nil, &unreadOnly))
	step("unread_only returns one", boolErr(len(unreadOnly.Mail) == 1,
		"got %d, want 1", len(unreadOnly.Mail)))

	// Claim the attachment. The response reports the resulting balance, so a
	// client can update the wallet without a second call.
	var claimed struct {
		ClaimedAt string `json:"claimed_at"`
		Rewards   []struct {
			CurrencyID string `json:"currency_id"`
			Amount     int64  `json:"amount"`
			Balance    int64  `json:"balance"`
		} `json:"rewards"`
	}
	step("alice claims rewards", call("POST", "/v1/mailbox/"+s.mailID+"/claim", s.alice.Token, nil, &claimed))
	// 700 was left after phaseCurrency; +500 from the mail = 1200.
	step("claim credited 500 on top of 700", boolErr(len(claimed.Rewards) == 1 &&
		claimed.Rewards[0].Amount == 500 && claimed.Rewards[0].Balance == 1200,
		"rewards = %+v, want amount 500 balance 1200", claimed.Rewards))

	// Claiming again must be refused, or a player could farm one mail forever.
	step("second claim rejected", expectErr(call("POST", "/v1/mailbox/"+s.mailID+"/claim", s.alice.Token, nil, nil)))

	// The wallet agrees with the claim response — the payout is in the ledger,
	// not just in the reply.
	var balances struct {
		Balances []struct {
			Balance int64 `json:"balance"`
		} `json:"balances"`
	}
	step("wallet reflects the claim", call("GET", "/v1/currency", s.alice.Token, nil, &balances))
	step("balance is 1200", boolErr(len(balances.Balances) == 1 && balances.Balances[0].Balance == 1200,
		"balances = %+v, want 1200", balances.Balances))

	// Bob may not touch alice's mail: it was never delivered to him, so it is
	// a 404 rather than a 403 — he cannot even learn that it exists.
	step("bob cannot claim alice's mail", expectErr(call("POST", "/v1/mailbox/"+s.mailID+"/claim", s.bob.Token, nil, nil)))
	step("unknown mail is 404", expectErr(call("POST", "/v1/mailbox/00000000-0000-0000-0000-000000000000/read",
		s.alice.Token, nil, nil)))

	// Bob still sees the broadcast, which needed no recipient row.
	var bobInbox struct {
		Mail []struct {
			Title string `json:"title"`
		} `json:"mail"`
	}
	step("bob sees the broadcast", call("GET", "/v1/mailbox", s.bob.Token, nil, &bobInbox))
	step("bob has exactly the broadcast", boolErr(len(bobInbox.Mail) == 1 && bobInbox.Mail[0].Title == "维护补偿",
		"bob inbox = %+v", bobInbox.Mail))

	// Admin view carries the delivery and engagement counters.
	var adminList struct {
		Mail []struct {
			Title      string `json:"title"`
			Recipients int    `json:"recipients"`
			ReadCount  int    `json:"read_count"`
			Claimed    int    `json:"claimed"`
		} `json:"mail"`
	}
	step("admin lists mail", call("GET", "/admin/api/games/"+s.gameID+"/mail", s.adminToken, nil, &adminList))
	step("admin list has both mail", boolErr(len(adminList.Mail) == 2, "got %d, want 2", len(adminList.Mail)))
}
