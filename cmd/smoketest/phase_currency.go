package main

import "fmt"

// phaseCurrency exercises virtual currencies end to end: the admin defines
// one, grants to a player, the player spends it, and the ledger records both.
// The point of the phase is idempotency — a grant or spend replayed with the
// same key must not move the balance a second time, because that is what lets
// a client retry after a dropped connection.
func phaseCurrency(s *state) {
	fmt.Println("\n-- currency --")

	// Admin defines a currency for this game.
	var coin struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	step("admin create currency", call("POST", "/admin/api/games/"+s.gameID+"/currencies", s.adminToken,
		map[string]any{"name": "gold", "display_name": "金币", "icon_url": ""}, &coin))
	s.currencyID = coin.ID // phaseMailbox pays its rewards in this currency

	// A second currency with the same name collides on the unique index.
	step("duplicate currency name rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/currencies", s.adminToken,
		map[string]any{"name": "gold"}, nil)))

	// Grant 1000 to alice. The response carries the resulting balance.
	type txRes struct {
		CurrencyID string `json:"currency_id"`
		Balance    int64  `json:"balance"`
		Duplicate  bool   `json:"duplicate"`
	}
	grantPath := "/admin/api/games/" + s.gameID + "/currency/" + s.currencyID + "/grant"
	var granted txRes
	step("admin grant 1000 to alice", call("POST", grantPath, s.adminToken,
		map[string]any{"player_id": s.alice.Player.ID, "amount": 1000,
			"idempotency_key": "grant-" + s.suffix, "note": "冒烟测试发放"}, &granted))
	step("balance is 1000 after grant", boolErr(granted.Balance == 1000 && !granted.Duplicate,
		"got %+v, want balance 1000 duplicate false", granted))

	// The same key again must report the original outcome, not add 1000 more.
	var replay txRes
	step("replayed grant is idempotent", call("POST", grantPath, s.adminToken,
		map[string]any{"player_id": s.alice.Player.ID, "amount": 1000,
			"idempotency_key": "grant-" + s.suffix, "note": "冒烟测试发放"}, &replay))
	step("replay reports duplicate, balance unchanged", boolErr(replay.Duplicate && replay.Balance == 1000,
		"got %+v, want balance 1000 duplicate true", replay))

	// Same key with a different amount is a conflict: the server cannot tell
	// which of the two the caller meant, so it refuses rather than guessing.
	step("same key different amount rejected", expectErr(call("POST", grantPath, s.adminToken,
		map[string]any{"player_id": s.alice.Player.ID, "amount": 500,
			"idempotency_key": "grant-" + s.suffix}, nil)))

	// Player reads the balance back through the client API.
	var balances struct {
		Balances []struct {
			CurrencyID  string `json:"currency_id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			Balance     int64  `json:"balance"`
		} `json:"balances"`
	}
	step("alice reads balances", call("GET", "/v1/currency", s.alice.Token, nil, &balances))
	step("alice sees 1000 gold", boolErr(len(balances.Balances) == 1 &&
		balances.Balances[0].Balance == 1000 && balances.Balances[0].Name == "gold",
		"balances = %+v", balances.Balances))

	// Spend 300 of it.
	spendPath := "/v1/currency/" + s.currencyID + "/spend"
	var spent txRes
	step("alice spends 300", call("POST", spendPath, s.alice.Token,
		map[string]any{"amount": 300, "idempotency_key": "spend-" + s.suffix, "note": "买了顶帽子"}, &spent))
	step("balance is 700 after spend", boolErr(spent.Balance == 700, "balance = %d, want 700", spent.Balance))

	// Replaying the spend must not charge twice — the retry-safety guarantee.
	var spentAgain txRes
	step("replayed spend is idempotent", call("POST", spendPath, s.alice.Token,
		map[string]any{"amount": 300, "idempotency_key": "spend-" + s.suffix, "note": "买了顶帽子"}, &spentAgain))
	step("replay still 700, duplicate true", boolErr(spentAgain.Balance == 700 && spentAgain.Duplicate,
		"got %+v, want balance 700 duplicate true", spentAgain))

	// Overspending is refused and leaves the balance alone.
	step("overspend rejected", expectErr(call("POST", spendPath, s.alice.Token,
		map[string]any{"amount": 99999, "idempotency_key": "overspend-" + s.suffix}, nil)))
	step("zero amount rejected", expectErr(call("POST", spendPath, s.alice.Token,
		map[string]any{"amount": 0, "idempotency_key": "zero-" + s.suffix}, nil)))
	step("missing idempotency key rejected", expectErr(call("POST", spendPath, s.alice.Token,
		map[string]any{"amount": 10}, nil)))

	// The ledger is the audit trail: one grant and one spend, newest first,
	// each carrying the balance that resulted from it.
	var ledger struct {
		Entries []struct {
			Amount       int64  `json:"amount"`
			BalanceAfter int64  `json:"balance_after"`
			Kind         string `json:"kind"`
		} `json:"entries"`
	}
	step("alice reads ledger", call("GET", "/v1/currency/"+s.currencyID+"/ledger", s.alice.Token, nil, &ledger))
	step("ledger has grant and spend", boolErr(len(ledger.Entries) == 2, "got %d entries, want 2", len(ledger.Entries)))
	if len(ledger.Entries) == 2 {
		step("ledger records the spend as negative", boolErr(
			ledger.Entries[0].Amount == -300 && ledger.Entries[0].BalanceAfter == 700,
			"newest entry = %+v, want amount -300 balance_after 700", ledger.Entries[0]))
	}

	// Bob has no wallet in this currency, so spending is refused rather than
	// silently creating a negative balance.
	step("bob cannot spend what he lacks", expectErr(call("POST", spendPath, s.bob.Token,
		map[string]any{"amount": 1, "idempotency_key": "bob-" + s.suffix}, nil)))

	// A currency from another game must not be reachable through this token.
	step("unknown currency is 404", expectErr(call("POST", "/v1/currency/00000000-0000-0000-0000-000000000000/spend",
		s.alice.Token, map[string]any{"amount": 1, "idempotency_key": "nope-" + s.suffix}, nil)))

	// Admin-side views of the same wallet.
	step("admin reads player balances", call("GET", "/admin/api/players/"+s.alice.Player.ID+"/currency", s.adminToken, nil, nil))
	step("admin reads player ledger",
		call("GET", "/admin/api/players/"+s.alice.Player.ID+"/currency/"+s.currencyID+"/ledger", s.adminToken, nil, nil))
	step("admin lists currencies", call("GET", "/admin/api/games/"+s.gameID+"/currencies", s.adminToken, nil, nil))
}
