# zekumo-sdk-go

Official Go SDK for [Zekumo](../README.md), the mini-game BaaS platform.
Covers every player API plus the realtime WebSocket gateway, and runs on
**Windows, Linux and macOS** (amd64 and arm64) with one dependency,
`gorilla/websocket`.

Built for desktop game clients, game servers, updaters and ops tooling — the
places where the browser JS SDK does not fit.

```bash
go get github.com/zekumo/sdk-go
```

## Quick start

```go
package main

import (
    "context"
    "log"

    zekumo "github.com/zekumo/sdk-go"
)

func main() {
    ctx := context.Background()
    mc := zekumo.New(zekumo.Options{
        AppID:   "zk_xxx",                     // safe to ship in a client
        BaseURL: "https://api.example.com",
    })

    // Guest login registers the player on first sight.
    login, err := mc.Auth.LoginAsGuest(ctx, deviceID(), "")
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("playing as %s (%s)", login.Player.Nickname, login.Player.ID)

    // Saves are JSON: pass any value, read it back into any type.
    if _, err := mc.Data.Set(ctx, "save1", map[string]any{"level": 3}); err != nil {
        log.Fatal(err)
    }
    var save struct{ Level int `json:"level"` }
    if err := mc.Data.Get(ctx, "save1", &save); err != nil {
        log.Fatal(err)
    }

    // Submit returns the score after the write, which differs under "incr".
    score, err := mc.Leaderboards.Submit(ctx, "weekly", 1200, "max")
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("score now %d", score)
}
```

Every call takes a `context.Context`, so a game can cancel in-flight requests
on quit instead of waiting out a timeout.

## Handling errors

API errors arrive as `*zekumo.Error` carrying the server's stable `Code`.
Branch on the code, never on the message:

```go
_, err := mc.Currency.Spend(ctx, coinID, 50, txnID, "bought a hat")
switch {
case zekumo.IsCode(err, "insufficient_funds"): // HTTP 402
    showShop()
case zekumo.IsCode(err, "player_banned"):        // HTTP 403
    showBanNotice()
case err != nil:
    log.Print(err)
}
```

`Spend` and every grant are **idempotent**: pass a unique
`idempotency_key` per transaction and a retry after a dropped connection is
safe — the result reports `Duplicate: true` rather than charging twice.

## Realtime

One WebSocket carries room sync and chat. Reconnection, backoff and the
server's heartbeat are handled for you:

```go
rt := mc.Realtime()

// welcome fires on connect AND after every reconnect — rejoin here, since
// rooms are server state the client does not own.
rt.On("welcome", func(json.RawMessage) { rt.JoinRoom(roomID) })

rt.On("room.state", func(data json.RawMessage) {
    var ev struct {
        PlayerID string          `json:"player_id"`
        State    json.RawMessage `json:"state"`
    }
    json.Unmarshal(data, &ev)
    updatePlayer(ev.PlayerID, ev.State)
})

rt.On("chat.msg", func(data json.RawMessage) {
    var m zekumo.ChatMessage
    json.Unmarshal(data, &m)
    appendToChat(m.SenderName, m.Content)
})

if err := rt.Connect(ctx); err != nil {
    log.Fatal(err)
}
defer rt.Close()

rt.SubscribeChat("world")
rt.SyncState(map[string]any{"x": 10, "y": 4})
```

Backoff climbs 1s → 2s → 4s … capped at 30s, so a server restart is not
stampeded by every client at once. Handlers run on the read goroutine — hand
long work to a channel your game loop drains.

**One connection per player.** If the same player connects elsewhere, this
connection is closed, `"replaced"` fires, and the client stops reconnecting
(fighting for the slot would loop forever). `rt.Err()` then returns
`zekumo.ErrReplaced`.

Events: `welcome`, `pong`, `reconnecting`, `replaced`, `room.created`,
`room.joined`, `room.left`, `room.list`, `room.member_joined`,
`room.member_left`, `room.state`, `room.msg`, `chat.subbed`,
`chat.unsubbed`, `chat.msg`, `error`.

## Updates (for an updater binary)

These endpoints need no token, because an updater runs before login:

```go
mc := zekumo.New(zekumo.Options{AppID: appID, BaseURL: baseURL})
upd, err := mc.Updates.Check(ctx, zekumo.CheckOptions{
    Version:  "1.0.0",
    Platform: "windows",  // windows | macos | linux | android | ios | web | any
    Arch:     "amd64",    // amd64 | arm64 | any
    DeviceID: machineID(), // keeps staged-rollout decisions stable per machine
})
if upd.UpdateAvailable {
    download(upd.Artifact.URL)
    // Always verify before installing.
    if sha256File(path) != upd.Artifact.SHA256 {
        log.Fatal("checksum mismatch")
    }
}
```

## Game-level KV

Reads use a player token and are limited to the `public` namespace and
`public_`-prefixed ones — for activity switches and global thresholds:

```go
var cfg struct{ DoubleDrop bool `json:"double_drop"` }
err := mc.KV.Get(ctx, "public", "event", &cfg)
```

Writes are signed with `app_secret`, which **must never ship in a game
client**. Use `Signer` from your own server or an ops tool:

```go
sg := zekumo.NewSigner(baseURL, appID, os.Getenv("ZEKUMO_APP_SECRET"))
sg.Put(ctx, "public", "event", map[string]any{"double_drop": true}, 0)
sg.Put(ctx, "public", "flash_sale", cfg, 2*time.Hour) // expires by itself
sg.Delete(ctx, "public", "event")
```

A rejected signature is almost always a clock more than 5 minutes off the
server's.

## Covered APIs

| Group | Methods |
|---|---|
| `Auth` | `LoginAsGuest` `LoginWithPassword` `LoginWithTicket` `Register` `Logout` |
| `Player` | `Me` `Update` `Bind` |
| `Data` | `List` `Get` `Set` `Delete` |
| `Leaderboards` | `Submit` `Top` (incl. `Scope: "friends"`) `Me` |
| `Achievements` | `List` `Unlocked` |
| `Friends` | `Request` `Accept` `Decline` `List` `Requests` `Remove` `Block` |
| `Currency` | `Balances` `Ledger` `Spend` |
| `Mailbox` | `Inbox` `Read` `Claim` |
| `Announce` | `List` (no token) |
| `Functions` | `Call` `CallPublic` |
| `Updates` | `Check` `Releases` (no token) |
| `Logs` | `Report` |
| `Dialogues` | `List` `Get` |
| `Chat` | `History` |
| `KV` | `Get` `List` (+ `Signer` for writes) |
| `Realtime` | rooms, state sync, chat, auto-reconnect |

Unlocking achievements and granting currency are deliberately absent: both are
server-side only, so a client cannot award itself anything.

## Session persistence

`Client` holds the token and is safe to share across goroutines. Save it to
skip a login on next launch:

```go
saveToDisk(mc.Token())
// next launch
mc := zekumo.New(zekumo.Options{AppID: appID, BaseURL: url, Token: saved})
```

A token outlives the process but not forever; on `401` (`IsCode(err,
"unauthorized")`) log in again.

## Testing

```bash
go test ./...          # what the SDK puts on the wire, against httptest
go test -race ./...    # the token and handler maps are shared state
```

## Build for three platforms

Pure Go, so one machine cross-compiles everything:

```bash
GOOS=windows GOARCH=amd64 go build ./...
GOOS=darwin  GOARCH=arm64 go build ./...
GOOS=linux   GOARCH=amd64 go build ./...
```
