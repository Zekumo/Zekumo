package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

func phaseUpdates(s *state) {
	// Update distribution: publish a release, check for updates, download,
	// verify revoke falls back to the previous version.
	payload := []byte("fake game build v1.0.0 -- " + time.Now().Format(time.RFC3339Nano))
	sum := sha256.Sum256(payload)
	relBase := "/admin/api/games/" + s.gameID + "/releases"

	step("create release 1.0.0", call("POST", relBase, s.adminToken,
		map[string]any{"version": "1.0.0", "changelog": "首个版本"}, nil))
	step("duplicate version rejected", expectErr(call("POST", relBase, s.adminToken,
		map[string]any{"version": "1.0.0"}, nil)))
	step("publish without artifacts rejected", expectErr(call("POST", relBase+"/1.0.0/publish", s.adminToken, nil, nil)))

	var artRes struct {
		Artifact  struct{ ID int64 }
		UploadURL string `json:"upload_url"`
	}
	step("register artifact", call("POST", relBase+"/1.0.0/artifacts", s.adminToken, map[string]any{
		"platform": "windows", "arch": "any", "filename": "game-1.0.0.zip",
		"size": len(payload), "sha256": hex.EncodeToString(sum[:]),
	}, &artRes))
	step("upload artifact bytes", upload(artRes.UploadURL, payload))
	step("complete artifact", call("POST", fmt.Sprintf("%s/1.0.0/artifacts/%d/complete", relBase, artRes.Artifact.ID),
		s.adminToken, nil, nil))
	step("publish 1.0.0", call("POST", relBase+"/1.0.0/publish", s.adminToken, nil, nil))

	type checkRes struct {
		UpdateAvailable bool `json:"update_available"`
		Release         struct {
			Version string `json:"version"`
		} `json:"release"`
		Artifact struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"artifact"`
	}
	checkPath := "/v1/apps/" + s.appID + "/updates/check?platform=windows&version="
	var chk checkRes
	step("check update from 0.9.0", call("GET", checkPath+"0.9.0", "", nil, &chk))
	if !chk.UpdateAvailable || chk.Release.Version != "1.0.0" || chk.Artifact.SHA256 != hex.EncodeToString(sum[:]) {
		step("update offered correctly", fmt.Errorf("unexpected check response: %+v", chk))
	} else {
		step("update offered correctly", nil)
	}
	step("download matches sha256", download(chk.Artifact.URL, sum[:]))

	var upToDate checkRes
	step("check update from 1.0.0", call("GET", checkPath+"1.0.0", "", nil, &upToDate))
	step("no update when current", boolErr(!upToDate.UpdateAvailable, "unexpected update offered: %+v", upToDate))

	// Publish 1.1.0 then revoke it: checks must fall back to 1.0.0.
	step("create release 1.1.0", call("POST", relBase, s.adminToken, map[string]any{"version": "1.1.0"}, nil))
	var art2 struct {
		Artifact  struct{ ID int64 }
		UploadURL string `json:"upload_url"`
	}
	step("register 1.1.0 artifact", call("POST", relBase+"/1.1.0/artifacts", s.adminToken, map[string]any{
		"platform": "windows", "arch": "any", "filename": "game-1.1.0.zip",
		"size": len(payload), "sha256": hex.EncodeToString(sum[:]),
	}, &art2))
	step("upload 1.1.0 bytes", upload(art2.UploadURL, payload))
	step("complete 1.1.0", call("POST", fmt.Sprintf("%s/1.1.0/artifacts/%d/complete", relBase, art2.Artifact.ID),
		s.adminToken, nil, nil))
	step("publish 1.1.0", call("POST", relBase+"/1.1.0/publish", s.adminToken, nil, nil))

	var chk2 checkRes
	step("check offers 1.1.0", call("GET", checkPath+"0.9.0", "", nil, &chk2))
	step("newest version wins", boolErr(chk2.Release.Version == "1.1.0", "got %q, want 1.1.0", chk2.Release.Version))

	step("revoke 1.1.0", call("POST", relBase+"/1.1.0/revoke", s.adminToken, nil, nil))
	var chk3 checkRes
	step("check after revoke", call("GET", checkPath+"0.9.0", "", nil, &chk3))
	step("revoke falls back to 1.0.0", boolErr(chk3.Release.Version == "1.0.0", "got %q, want 1.0.0", chk3.Release.Version))

	step("public release list", call("GET", "/v1/apps/"+s.appID+"/releases", "", nil, nil))
}
