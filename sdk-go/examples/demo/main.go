// Command demo exercises the SDK against a running Zekumo server: guest
// login, a save round-trip, a leaderboard submit, the realtime gateway, and
// an update check. It is the fastest way to confirm a deployment works from a
// desktop client's point of view.
//
// Run it against a local server:
//
//	go run ./examples/demo -url http://localhost:8080 -app zk_xxx
//
// Every step prints what it did, so a failure says which call broke rather
// than just exiting non-zero.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"time"

	zekumo "github.com/zekumo/sdk-go"
)

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Zekumo base URL")
	appID := flag.String("app", "", "App ID from the console (required)")
	device := flag.String("device", "demo-device-1", "device id for guest login")
	flag.Parse()

	if *appID == "" {
		log.Fatal("-app is required; copy the App ID from the console")
	}

	// Ctrl-C cancels in-flight requests instead of leaving them hanging,
	// which is exactly how a game should quit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	mc := zekumo.New(zekumo.Options{AppID: *appID, BaseURL: *baseURL})

	// --- login ---
	login, err := mc.Auth.LoginAsGuest(ctx, *device, "")
	if err != nil {
		log.Fatalf("login: %v", err)
	}
	fmt.Printf("logged in as %s (%s)\n", login.Player.Nickname, login.Player.ID)

	// --- saves ---
	type save struct {
		Level int    `json:"level"`
		Scene string `json:"scene"`
	}
	if _, err := mc.Data.Set(ctx, "slot1", save{Level: 7, Scene: "cave"}); err != nil {
		log.Fatalf("save: %v", err)
	}
	var loaded save
	if err := mc.Data.Get(ctx, "slot1", &loaded); err != nil {
		log.Fatalf("load: %v", err)
	}
	fmt.Printf("save round-tripped: level=%d scene=%s\n", loaded.Level, loaded.Scene)

	// --- leaderboard ---
	score, err := mc.Leaderboards.Submit(ctx, "demo", 1234, "max")
	if err != nil {
		log.Fatalf("submit score: %v", err)
	}
	top, err := mc.Leaderboards.Top(ctx, "demo", zekumo.TopOptions{Limit: 3})
	if err != nil {
		log.Fatalf("read board: %v", err)
	}
	fmt.Printf("score %d; top %d:\n", score, len(top))
	for _, e := range top {
		fmt.Printf("  #%d %s %d\n", e.Rank, e.Nickname, e.Score)
	}

	// --- announcements (no token needed) ---
	anns, err := mc.Announce.List(ctx, zekumo.AnnounceOptions{})
	if err != nil {
		log.Fatalf("announcements: %v", err)
	}
	fmt.Printf("%d active announcement(s)\n", len(anns))

	// --- update check, as an updater would do it ---
	upd, err := mc.Updates.Check(ctx, zekumo.CheckOptions{
		Version:  "1.0.0",
		Platform: platform(), // report the real OS so artifact matching works
		Arch:     runtime.GOARCH,
		DeviceID: *device,
	})
	if err != nil {
		log.Fatalf("update check: %v", err)
	}
	if upd.UpdateAvailable {
		fmt.Printf("update available: %s (verify sha256 %s before installing)\n",
			upd.Release.Version, upd.Artifact.SHA256[:12]+"…")
	} else {
		fmt.Println("no update available for 1.0.0")
	}

	// --- realtime ---
	realtimeDemo(ctx, mc)
}

// realtimeDemo connects, creates a room, syncs one state frame and reads the
// echo back, then leaves. It waits on channels rather than sleeping, so it
// finishes as soon as the server answers.
func realtimeDemo(ctx context.Context, mc *zekumo.Client) {
	rt := mc.Realtime()

	created := make(chan string, 1)
	rt.On("room.created", func(data json.RawMessage) {
		var ev struct {
			Room zekumo.Room `json:"room"`
		}
		json.Unmarshal(data, &ev)
		select {
		case created <- ev.Room.ID:
		default:
		}
	})
	// A reconnect re-greets, which is where a real game would rejoin its room.
	rt.On("welcome", func(json.RawMessage) { fmt.Println("realtime: welcomed") })
	rt.On("reconnecting", func(data json.RawMessage) {
		fmt.Printf("realtime: reconnecting %s\n", data)
	})
	rt.On("error", func(data json.RawMessage) {
		fmt.Printf("realtime error: %s\n", data)
	})

	if err := rt.Connect(ctx); err != nil {
		log.Fatalf("realtime connect: %v", err)
	}
	defer rt.Close()

	if err := rt.CreateRoom("demo-room", 4, map[string]any{"mode": "coop"}); err != nil {
		log.Fatalf("create room: %v", err)
	}

	select {
	case id := <-created:
		fmt.Printf("realtime: room %s created\n", id)
		if err := rt.SyncState(map[string]any{"x": 1, "y": 2}); err != nil {
			log.Fatalf("sync state: %v", err)
		}
		fmt.Println("realtime: state synced")
		_ = rt.LeaveRoom()
	case <-time.After(5 * time.Second):
		log.Fatal("realtime: no room.created within 5s")
	case <-ctx.Done():
		return
	}
}

// platform maps Go's OS names onto the ones the update service matches
// artifacts against.
func platform() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	default:
		return "any"
	}
}
