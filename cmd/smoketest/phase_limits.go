package main

import "strings"

func phaseLimits(s *state) {
	// Per-account lockout: 10 failures against one identity must lock that
	// account, which a per-IP limit cannot do. Deliberately not tripping an
	// IP-keyed rule here — that counter is shared, and would make re-running
	// this test within the same minute fail at its first login.
	lockSuffix := s.suffix + "b"
	var locked bool
	var locker acctRes
	step("register account for lockout test", call("POST", "/sso/api/register", "",
		map[string]string{"username": "lock_" + lockSuffix, "password": "hunter44"}, &locker))
	s.accountIDs = append(s.accountIDs, locker.Account.ID)
	// Link the account to this workspace's game so the tenant owner can clean
	// up the fixture without gaining access to unrelated platform accounts.
	step("link lockout account to game", call("POST", "/v1/auth/login", "",
		map[string]string{"app_id": s.appID, "provider": "sso", "username": "lock_" + lockSuffix, "password": "hunter44"}, nil))
	for range 12 {
		err := call("POST", "/sso/api/login", "",
			map[string]string{"username": "lock_" + lockSuffix, "password": "wrong-one"}, nil)
		if err != nil && strings.Contains(err.Error(), "locked") {
			locked = true
			break
		}
	}
	step("account locks after repeated failures", boolErr(locked, "account never locked"))
	step("correct password still refused while locked", expectErr(call("POST", "/sso/api/login", "",
		map[string]string{"username": "lock_" + lockSuffix, "password": "hunter44"}, nil)))
}
