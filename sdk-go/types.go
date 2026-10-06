// Package zekumo is the official Go SDK for Zekumo, the mini-game BaaS
// platform. It covers every player-facing API plus the realtime WebSocket
// gateway, and runs on Windows, Linux and macOS with one dependency
// (gorilla/websocket).
//
// Typical use from a desktop game client:
//
//	mc := zekumo.New(zekumo.Options{
//	    AppID:   "zk_xxx",
//	    BaseURL: "https://api.example.com",
//	})
//	login, err := mc.Auth.LoginAsGuest(ctx, deviceID, "")
//	err = mc.Data.Set(ctx, "save1", map[string]any{"level": 3})
//
// Every call takes a context.Context, so a game loop can cancel in-flight
// requests on quit rather than waiting out a network timeout.
//
// This file holds the wire types: one struct per JSON shape the server
// returns. Field names and tags mirror the server's own structs, so adding a
// field on the server only means adding it here.
package zekumo

import (
	"encoding/json"
	"time"
)

// ---------- player ----------

// Player is the profile the server keeps for one player of one game.
type Player struct {
	ID          string          `json:"id"`
	GameID      string          `json:"game_id"`
	Provider    string          `json:"provider"`   // guest | password | sso
	Identifier  string          `json:"identifier"` // device id, username, or account id
	Nickname    string          `json:"nickname"`
	Profile     json.RawMessage `json:"profile"` // game-defined blob, left raw
	Banned      bool            `json:"banned"`
	AccountID   string          `json:"account_id,omitempty"` // set once bound to a platform account
	CreatedAt   time.Time       `json:"created_at"`
	LastLoginAt time.Time       `json:"last_login_at"`
}

// Login is what a successful login returns: the JWT plus who you are. The SDK
// stores the token itself, so callers rarely read Token.
type Login struct {
	Token  string `json:"token"`
	Player Player `json:"player"`
}

// Entry is one save slot. Value is nil in list responses (the server omits
// bodies there) and populated by Get.
type Entry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ---------- leaderboards ----------

// Score is one row of a leaderboard. Rank is 1-based.
type Score struct {
	Rank     int    `json:"rank"`
	PlayerID string `json:"player_id"`
	Nickname string `json:"nickname"`
	Score    int64  `json:"score"`
}

// ---------- achievements ----------

// Achievement carries both the definition and this player's progress, so one
// list call is enough to draw an achievement screen.
type Achievement struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IconURL     string     `json:"icon_url"`
	Rarity      string     `json:"rarity"` // common | rare | epic | legendary
	Hidden      bool       `json:"hidden"` // name and description stay blank until unlocked
	Type        string     `json:"type"`   // instant | progress
	Target      int        `json:"target"` // progress achievements unlock at this value
	SortOrder   int        `json:"sort_order"`
	Unlocked    bool       `json:"unlocked"`
	Progress    int        `json:"progress"`
	UnlockedAt  *time.Time `json:"unlocked_at"` // nil while locked
}

// ---------- friends ----------

// Friend is an established friendship, with presence from the WebSocket
// gateway (30s accuracy).
type Friend struct {
	PlayerID    string    `json:"player_id"`
	Nickname    string    `json:"nickname"`
	Online      bool      `json:"online"`
	FriendSince time.Time `json:"friend_since"`
}

// FriendRequest is a pending invitation in either direction; the nickname
// filled in depends on which side of it you are.
type FriendRequest struct {
	ID           string    `json:"id"`
	GameID       string    `json:"game_id"`
	FromID       string    `json:"from_id"`
	ToID         string    `json:"to_id"`
	Status       string    `json:"status"` // pending | accepted | declined
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	FromNickname string    `json:"from_nickname,omitempty"`
	ToNickname   string    `json:"to_nickname,omitempty"`
}

// ---------- currency ----------

// Balance is one currency the player holds, with enough display metadata to
// render a wallet without a second lookup.
type Balance struct {
	CurrencyID  string `json:"currency_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	IconURL     string `json:"icon_url"`
	Balance     int64  `json:"balance"`
}

// Ledger is one line of the audit trail. BalanceAfter lets a client show a
// running total without recomputing it.
type Ledger struct {
	ID             int64     `json:"id"`
	CurrencyID     string    `json:"currency_id"`
	PlayerID       string    `json:"player_id"`
	Amount         int64     `json:"amount"` // negative for spends
	BalanceAfter   int64     `json:"balance_after"`
	Kind           string    `json:"kind"`
	IdempotencyKey string    `json:"idempotency_key"`
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

// Spend reports the balance after a spend. Duplicate is true when this
// idempotency key was already applied — the spend did not happen twice, and
// this is a success, not an error.
type Spend struct {
	CurrencyID string `json:"currency_id"`
	Balance    int64  `json:"balance"`
	Duplicate  bool   `json:"duplicate"`
}

// ---------- mailbox ----------

// Reward is one currency attachment on a mail.
type Reward struct {
	CurrencyID string `json:"currency_id"`
	Amount     int64  `json:"amount"`
}

// Mail is one inbox item. ReadAt and ClaimedAt are nil until the player acts,
// which is how a client decides whether to show an unread dot or a claim
// button.
type Mail struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Rewards   []Reward   `json:"rewards"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
}

// Claim is the result of claiming attachments: what was granted and the
// resulting balance of each currency.
type Claim struct {
	ClaimedAt time.Time `json:"claimed_at"`
	Rewards   []struct {
		CurrencyID string `json:"currency_id"`
		Amount     int64  `json:"amount"`
		Balance    int64  `json:"balance"`
	} `json:"rewards"`
}

// ---------- announcements ----------

// Announcement is a server notice. Body is Markdown.
type Announcement struct {
	ID         string     `json:"id"`
	GameID     string     `json:"game_id"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Importance string     `json:"importance"` // info | warning | critical
	Platform   string     `json:"platform"`   // empty means every platform
	Channel    string     `json:"channel"`    // empty means every channel
	Active     bool       `json:"active"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ---------- cloud functions ----------

// FuncResult is a cloud function's return value plus anything it logged,
// which is what makes a failing function debuggable from the client.
type FuncResult struct {
	Result json.RawMessage `json:"result"`
	Logs   []string        `json:"logs"`
}

// ---------- updates ----------

// Update answers "is there a newer build?". Release and Artifact are nil when
// UpdateAvailable is false.
type Update struct {
	UpdateAvailable bool `json:"update_available"`
	Mandatory       bool `json:"mandatory"` // forced build, or client below min_supported_version
	Release         *struct {
		Version     string    `json:"version"`
		Changelog   string    `json:"changelog"`
		PublishedAt time.Time `json:"published_at"`
	} `json:"release,omitempty"`
	Artifact *struct {
		URL      string `json:"url"` // presigned, short-lived
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"` // verify before installing
	} `json:"artifact,omitempty"`
}

// Release is one published version in the public history.
type Release struct {
	ID                  int64      `json:"id"`
	GameID              string     `json:"game_id"`
	Channel             string     `json:"channel"`
	Version             string     `json:"version"`
	Changelog           string     `json:"changelog"`
	Status              string     `json:"status"`
	Mandatory           bool       `json:"mandatory"`
	MinSupportedVersion string     `json:"min_supported_version,omitempty"`
	RolloutPercent      int        `json:"rollout_percent"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// ---------- logs ----------

// Log is a client-side log line shipped to the server for crash triage.
type Log struct {
	Level   string         `json:"level,omitempty"` // debug | info | warn | error
	Event   string         `json:"event,omitempty"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// ---------- dialogues ----------

// Dialogue is a story script authored in the console. Content is nil in list
// responses and populated when fetched by key.
type Dialogue struct {
	ID        string          `json:"id"`
	GameID    string          `json:"game_id"`
	ScriptKey string          `json:"script_key"`
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content,omitempty"`
	Version   int             `json:"version"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ---------- chat ----------

// ChatMessage is one persisted chat line.
type ChatMessage struct {
	ID         int64     `json:"id"`
	GameID     string    `json:"game_id"`
	Channel    string    `json:"channel"`
	SenderID   string    `json:"sender_id"`
	SenderName string    `json:"sender_name"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// ---------- game-level KV ----------

// KV is one game-level config value.
type KV struct {
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
}

// KVKey is one entry of a namespace listing (keys only, no values).
type KVKey struct {
	Key       string    `json:"key"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------- realtime ----------

// Member is one player in a room, with whatever state they last synced.
type Member struct {
	PlayerID string          `json:"player_id"`
	Nickname string          `json:"nickname"`
	State    json.RawMessage `json:"state,omitempty"`
}

// Room is the shared state of one realtime room.
type Room struct {
	Locked     bool            `json:"locked"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	OwnerID    string          `json:"owner_id"`
	MaxPlayers int             `json:"max_players"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	Members    []Member        `json:"members"`
}
