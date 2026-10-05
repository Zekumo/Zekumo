package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// exportWait bounds how long a job may take before the phase gives up. Jobs
// run in a background goroutine, so the phase has to poll rather than read a
// result from the submit response.
const exportWait = 30 * time.Second

// phaseExports exercises data export: an async job that reads a game's player
// data out of Postgres, writes JSON or zipped CSV to blob storage, and hands
// back a presigned download link. The phase submits both formats and both
// scopes, then downloads and parses what came out — a job that reports "done"
// but produced an unreadable file is still a failure.
func phaseExports(s *state) {
	fmt.Println("\n-- data exports --")

	// History starts empty; an empty array, not null, so the console can
	// iterate it.
	var empty struct {
		Exports []any `json:"exports"`
	}
	step("export history empty initially", call("GET", "/admin/api/games/"+s.gameID+"/exports", s.adminToken, nil, &empty))
	step("no exports yet", boolErr(len(empty.Exports) == 0, "got %d, want 0", len(empty.Exports)))

	// Validation happens before a job is queued, so a bad request costs
	// nothing.
	step("bad format rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/exports", s.adminToken,
		map[string]any{"format": "xml", "scope": "all"}, nil)))
	step("bad scope rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/exports", s.adminToken,
		map[string]any{"format": "json", "scope": "everything"}, nil)))
	step("player scope without player_id rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/exports", s.adminToken,
		map[string]any{"format": "json", "scope": "player"}, nil)))
	step("unknown player rejected", expectErr(call("POST", "/admin/api/games/"+s.gameID+"/exports", s.adminToken,
		map[string]any{"format": "json", "scope": "player",
			"player_id": "00000000-0000-0000-0000-000000000000"}, nil)))

	// --- whole-game JSON export ---

	jsonJob := submitExport(s, "json", "all", "")
	jsonDone := awaitExport(s, jsonJob, "json/all")
	if jsonDone != "" {
		body, err := fetchExport(jsonDone)
		step("download json export", err)
		if err == nil {
			// The export is an envelope around the player list, so a reader
			// knows which game and scope the file came from.
			var doc struct {
				GameID      string `json:"game_id"`
				Scope       string `json:"scope"`
				GeneratedAt string `json:"generated_at"`
				Players     []struct {
					ID       string `json:"id"`
					Nickname string `json:"nickname"`
					Data     []struct {
						Key string `json:"key"`
					} `json:"data"`
					Balances []struct {
						Currency string `json:"currency"`
						Balance  int64  `json:"balance"`
					} `json:"balances"`
					Ledger []struct {
						Kind string `json:"kind"`
					} `json:"ledger"`
				} `json:"players"`
			}
			step("json export parses", json.Unmarshal(body, &doc))
			step("json export is labelled with the game and scope", boolErr(
				doc.GameID == s.gameID && doc.Scope == "all" && doc.GeneratedAt != "",
				"envelope = game %q scope %q at %q", doc.GameID, doc.Scope, doc.GeneratedAt))
			players := doc.Players
			// Earlier phases intentionally create SSO-backed players too; the
			// whole-game export must include at least the two core fixtures.
			step("json export has the core players", boolErr(len(players) >= 2, "got %d players, want at least 2", len(players)))

			var alice struct {
				found   bool
				saves   int
				gold    int64
				ledgerN int
			}
			for _, p := range players {
				if p.ID != s.alice.Player.ID {
					continue
				}
				alice.found = true
				alice.saves = len(p.Data)
				alice.ledgerN = len(p.Ledger)
				for _, b := range p.Balances {
					if b.Currency == "gold" {
						alice.gold = b.Balance
					}
				}
			}
			step("export includes alice", boolErr(alice.found, "alice not in the export"))
			step("export carries her save data", boolErr(alice.saves >= 1, "saves = %d, want >= 1", alice.saves))
			// 1000 granted - 300 spent + 500 mail = 1200, and three ledger lines.
			step("export carries her wallet", boolErr(alice.gold == 1200, "gold = %d, want 1200", alice.gold))
			step("export carries her ledger", boolErr(alice.ledgerN == 3, "ledger lines = %d, want 3", alice.ledgerN))
		}
	}

	// --- single-player CSV export ---

	csvJob := submitExport(s, "csv", "player", s.alice.Player.ID)
	csvDone := awaitExport(s, csvJob, "csv/player")
	if csvDone != "" {
		body, err := fetchExport(csvDone)
		step("download csv export", err)
		if err == nil {
			// CSV exports arrive as a zip with one file per table, so a
			// spreadsheet can open each without splitting anything.
			zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
			step("csv export is a readable zip", err)
			if err == nil {
				inZip := map[string]bool{}
				for _, f := range zr.File {
					inZip[f.Name] = true
				}
				want := []string{"players.csv", "player_data.csv", "balances.csv",
					"currency_ledger.csv", "achievements.csv"}
				missing := []string{}
				for _, n := range want {
					if !inZip[n] {
						missing = append(missing, n)
					}
				}
				step("zip has one csv per table", boolErr(len(missing) == 0, "missing %v", missing))

				// The player-scoped export must hold exactly one player row
				// (plus the header), or the scope filter is not doing its job.
				rows, err := csvRows(zr, "players.csv")
				step("read players.csv from the zip", err)
				step("player scope exported one player", boolErr(len(rows) == 2,
					"players.csv has %d lines, want header + 1", len(rows)))
				if len(rows) == 2 {
					step("the exported player is alice", boolErr(rows[1][0] == s.alice.Player.ID,
						"exported %q, want alice %q", rows[1][0], s.alice.Player.ID))
				}
			}
		}
	}

	// Status of an unknown job is a 404, not an empty job.
	step("unknown job is 404", expectErr(call("GET", "/admin/api/exports/00000000-0000-0000-0000-000000000000",
		s.adminToken, nil, nil)))

	// History now lists both jobs, newest first, each with a download link.
	var history struct {
		Exports []struct {
			Job struct {
				ID     string `json:"id"`
				Format string `json:"format"`
				Scope  string `json:"scope"`
				Status string `json:"status"`
				Size   int64  `json:"size"`
			} `json:"job"`
			DownloadURL string `json:"download_url"`
		} `json:"exports"`
	}
	step("export history lists both jobs", call("GET", "/admin/api/games/"+s.gameID+"/exports", s.adminToken, nil, &history))
	step("history has two jobs", boolErr(len(history.Exports) == 2, "got %d, want 2", len(history.Exports)))
	allLinked := len(history.Exports) == 2
	for _, e := range history.Exports {
		if e.Job.Status != "done" || e.DownloadURL == "" || e.Job.Size == 0 {
			allLinked = false
		}
	}
	step("every done job has a size and a link", boolErr(allLinked, "history = %+v", history.Exports))
}

// submitExport queues one job and returns its id.
func submitExport(s *state, format, scope, playerID string) string {
	body := map[string]any{"format": format, "scope": scope}
	if playerID != "" {
		body["player_id"] = playerID
	}
	var res struct {
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	step("submit "+format+"/"+scope+" export",
		call("POST", "/admin/api/games/"+s.gameID+"/exports", s.adminToken, body, &res))
	// The job is queued, not finished — the reply is 202 with a pending job.
	step(format+"/"+scope+" starts pending", boolErr(res.Job.Status == "pending" || res.Job.Status == "running",
		"status = %q, want pending or running", res.Job.Status))
	return res.Job.ID
}

// awaitExport polls until the job finishes, and returns its download URL. A
// failed job reports the server's own error message, which is what makes a
// broken export debuggable from the smoke test output.
func awaitExport(s *state, jobID, label string) string {
	if jobID == "" {
		step(label+" export completes", fmt.Errorf("no job id to poll"))
		return ""
	}
	deadline := time.Now().Add(exportWait)
	for time.Now().Before(deadline) {
		var res struct {
			Job struct {
				Status string `json:"status"`
				Error  string `json:"error"`
				Size   int64  `json:"size"`
			} `json:"job"`
			DownloadURL string `json:"download_url"`
		}
		if err := call("GET", "/admin/api/exports/"+jobID, s.adminToken, nil, &res); err != nil {
			step(label+" export completes", err)
			return ""
		}
		switch res.Job.Status {
		case "done":
			step(label+" export completes", nil)
			step(label+" export has a download link", boolErr(res.DownloadURL != "" && res.Job.Size > 0,
				"url = %q size = %d", res.DownloadURL, res.Job.Size))
			return res.DownloadURL
		case "failed":
			step(label+" export completes", fmt.Errorf("job failed: %s", res.Job.Error))
			return ""
		}
		time.Sleep(300 * time.Millisecond)
	}
	step(label+" export completes", fmt.Errorf("still unfinished after %s", exportWait))
	return ""
}

// csvRows reads one file out of the zip and parses it, header row included.
func csvRows(zr *zip.Reader, name string) ([][]string, error) {
	f, err := zr.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return csv.NewReader(f).ReadAll()
}

// fetchExport downloads the presigned URL and returns the bytes.
func fetchExport(rawURL string) ([]byte, error) {
	res, err := http.Get(rebase(rawURL))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("GET export -> %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}
