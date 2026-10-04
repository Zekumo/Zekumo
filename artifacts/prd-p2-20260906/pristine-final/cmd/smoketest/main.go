// Command smoketest exercises every MiniCloud API end to end against a
// running server. Each phase lives in its own file and receives the shared
// state (tokens and ids) established by the phases before it.
package main

import (
	"fmt"
	"os"
)

// state carries what one phase establishes and later phases need.
type state struct {
	adminToken string
	gameID     string
	appID      string
	alice      loginRes
	bob        loginRes
	suffix     string
	accountTok string // platform account (通行证) token
	accountUsr string
	fnBase     string // admin cloud-function path for this game

	// These are platform-wide, so deleting the game does not reach them;
	// cleanup has to remove them explicitly or every run leaves rows behind.
	oauthClientID string
	accountIDs    []string
}

type loginRes struct {
	Token  string `json:"token"`
	Player struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	} `json:"player"`
}

type acctRes struct {
	Token   string `json:"token"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

func main() {
	fmt.Println("== MiniCloud smoke test ==")
	s := &state{}
	phaseCore(s)

	if env("MINICLOUD_PHASE2_ONLY", "") == "true" {
		phaseFriends(s)
		phaseAchievements(s)
		phaseAnnouncements(s)
		phaseCleanup(s)
		finish()
		return
	}

	phaseRealtime(s)
	phaseUpdates(s)
	phaseSSO(s)
	phaseOAuth(s)
	phaseFunctions(s)
	phaseWebhooks(s)
	phaseObservability(s)
	phaseLimits(s)
	phaseFriends(s)
	phaseAchievements(s)
	phaseAnnouncements(s)
	phaseCleanup(s)
	finish()
}

func finish() {
	if failed > 0 {
		fmt.Printf("\n== %d step(s) FAILED ==\n", failed)
		os.Exit(1)
	}
	fmt.Println("\n== all steps passed ==")
}
