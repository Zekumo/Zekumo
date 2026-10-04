// api.go holds every player-facing API group. One group per server feature,
// each a thin named wrapper over a REST call, so a reader can match a method
// to the endpoint it hits without leaving the line.
//
//	mc.Auth.LoginAsGuest(ctx, deviceID, "")     -> POST /v1/auth/login
//	mc.Data.Set(ctx, "save1", payload)          -> PUT  /v1/player/data/save1
//	mc.Currency.Spend(ctx, cid, 50, key, "")    -> POST /v1/currency/{cid}/spend
//
// Optional parameters travel in an Options struct rather than a long argument
// list: adding a field later does not break existing call sites, and a zero
// value reliably means "not set" (the query builder drops empties).

package minicloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// esc escapes one path segment. Save keys, board names and namespaces are
// caller-supplied, and a slash in one would otherwise silently retarget the
// request at a different endpoint.
//
// url.PathEscape deliberately leaves "/" alone — it escapes a path, not a
// segment — so the slash is encoded here. url.QueryEscape is not a
// substitute: it turns a space into "+", which stays a literal plus in a
// path.
func esc(s string) string { return strings.ReplaceAll(url.PathEscape(s), "/", "%2F") }

// ---------- auth ----------

// AuthAPI logs players in. Every login stores the returned token on the
// Client, so no caller has to thread it through by hand.
type AuthAPI struct{ c *Client }

// login is the one place a token gets installed, so every provider below
// behaves identically on success.
func (a *AuthAPI) login(ctx context.Context, path string, body map[string]any) (*Login, error) {
	body["app_id"] = a.c.appID // every login is scoped to one game
	var out Login
	if err := a.c.public(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	a.c.SetToken(out.Token)
	return &out, nil
}

// LoginAsGuest logs in by device id, registering the player on first sight.
// An empty nickname lets the server generate one.
func (a *AuthAPI) LoginAsGuest(ctx context.Context, deviceID, nickname string) (*Login, error) {
	return a.login(ctx, "/v1/auth/login", map[string]any{
		"provider": "guest", "device_id": deviceID, "nickname": nickname,
	})
}

// LoginWithPassword logs in with credentials registered via Register.
func (a *AuthAPI) LoginWithPassword(ctx context.Context, username, password string) (*Login, error) {
	return a.login(ctx, "/v1/auth/login", map[string]any{
		"provider": "password", "username": username, "password": password,
	})
}

// LoginWithTicket exchanges a one-time SSO ticket from the hosted authorize
// page for a player token.
func (a *AuthAPI) LoginWithTicket(ctx context.Context, ticket string) (*Login, error) {
	return a.login(ctx, "/v1/auth/login", map[string]any{
		"provider": "sso", "ticket": ticket,
	})
}

// Register creates a password account and logs in as it.
func (a *AuthAPI) Register(ctx context.Context, username, password, nickname string) (*Login, error) {
	return a.login(ctx, "/v1/auth/register", map[string]any{
		"provider": "password", "username": username, "password": password, "nickname": nickname,
	})
}

// Logout forgets the token locally. Server-side JWTs stay valid until they
// expire, so a ban — not a logout — is what revokes access immediately.
func (a *AuthAPI) Logout() { a.c.SetToken("") }

// ---------- player profile ----------

// PlayerAPI reads and updates the player's own profile.
type PlayerAPI struct{ c *Client }

// Me returns the logged-in player's profile.
func (p *PlayerAPI) Me(ctx context.Context) (*Player, error) {
	var out Player
	return &out, p.c.get(ctx, "/v1/player/profile", &out)
}

// Update changes the nickname and/or the game-defined profile blob. Pass an
// empty nickname to leave it alone, nil profile to leave that alone.
func (p *PlayerAPI) Update(ctx context.Context, nickname string, profile any) (*Player, error) {
	body := map[string]any{}
	if nickname != "" {
		body["nickname"] = nickname
	}
	if profile != nil {
		body["profile"] = profile
	}
	var out Player
	return &out, p.c.put(ctx, "/v1/player/profile", body, &out)
}

// Bind attaches this player to a platform account (通行证), either by a
// one-time ticket or by account credentials.
func (p *PlayerAPI) Bind(ctx context.Context, ticket, username, password string) error {
	return p.c.post(ctx, "/v1/player/bind", map[string]any{
		"ticket": ticket, "username": username, "password": password,
	}, nil)
}

// ---------- saves ----------

// DataAPI is the player's key-value save storage.
type DataAPI struct{ c *Client }

// List returns every save slot's key and mtime. Values are omitted by the
// server here — fetch one with Get.
func (d *DataAPI) List(ctx context.Context) ([]Entry, error) {
	var out struct {
		Entries []Entry `json:"entries"`
	}
	return out.Entries, d.c.get(ctx, "/v1/player/data", &out)
}

// Get reads one save slot into v, which must be a pointer.
//
//	var save struct{ Level int `json:"level"` }
//	err := mc.Data.Get(ctx, "save1", &save)
func (d *DataAPI) Get(ctx context.Context, key string, v any) error {
	var out Entry
	if err := d.c.get(ctx, "/v1/player/data/"+esc(key), &out); err != nil {
		return err
	}
	return json.Unmarshal(out.Value, v)
}

// Set writes one save slot, replacing whatever was there.
func (d *DataAPI) Set(ctx context.Context, key string, value any) (*Entry, error) {
	var out Entry
	return &out, d.c.put(ctx, "/v1/player/data/"+esc(key), map[string]any{"value": value}, &out)
}

// Delete removes one save slot.
func (d *DataAPI) Delete(ctx context.Context, key string) error {
	return d.c.del(ctx, "/v1/player/data/"+esc(key), nil)
}

// ---------- leaderboards ----------

// LeaderboardsAPI submits scores and reads rankings.
type LeaderboardsAPI struct{ c *Client }

// TopOptions narrows a leaderboard read. Scope "friends" returns only the
// player's friends, which is the retention-driving view.
type TopOptions struct {
	Offset int
	Limit  int
	Scope  string // "" for global, "friends" for friends-only
}

// Submit posts a score and returns the player's score after it lands — which
// differs from what was passed in "incr" mode. mode is "max" (keep the best,
// the default when empty), "incr" (add to the existing score) or "replace".
func (l *LeaderboardsAPI) Submit(ctx context.Context, board string, score int64, mode string) (int64, error) {
	var out struct {
		Score int64 `json:"score"`
	}
	body := map[string]any{"score": score}
	if mode != "" {
		body["mode"] = mode
	}
	return out.Score, l.c.post(ctx, "/v1/leaderboards/"+esc(board)+"/score", body, &out)
}

// Top reads a page of the ranking.
func (l *LeaderboardsAPI) Top(ctx context.Context, board string, opts TopOptions) ([]Score, error) {
	var out struct {
		Entries []Score `json:"entries"`
	}
	q := query{"offset": itoa(opts.Offset), "limit": itoa(opts.Limit), "scope": opts.Scope}
	return out.Entries, l.c.get(ctx, "/v1/leaderboards/"+esc(board)+q.encode(), &out)
}

// Me returns the player's own rank on a board.
func (l *LeaderboardsAPI) Me(ctx context.Context, board string) (*Score, error) {
	var out Score
	return &out, l.c.get(ctx, "/v1/leaderboards/"+esc(board)+"/me", &out)
}

// ---------- achievements ----------

// AchievementsAPI reads achievement definitions and this player's progress.
// Unlocking is server-side only (cloud function or admin API) so a client
// cannot award itself anything.
type AchievementsAPI struct{ c *Client }

// List returns every achievement with this player's progress folded in.
// Hidden ones stay nameless until unlocked.
func (a *AchievementsAPI) List(ctx context.Context) ([]Achievement, error) {
	var out struct {
		Achievements []Achievement `json:"achievements"`
	}
	return out.Achievements, a.c.get(ctx, "/v1/achievements", &out)
}

// Unlocked returns only what this player has unlocked, newest first.
func (a *AchievementsAPI) Unlocked(ctx context.Context) ([]Achievement, error) {
	var out struct {
		Achievements []Achievement `json:"achievements"`
	}
	return out.Achievements, a.c.get(ctx, "/v1/achievements/unlocked", &out)
}

// ---------- friends ----------

// FriendsAPI manages the social graph: requests, the friend list with
// presence, removal and blocking.
type FriendsAPI struct{ c *Client }

// Request sends a friend request. A friendship needs both sides to agree.
func (f *FriendsAPI) Request(ctx context.Context, targetPlayerID string) (*FriendRequest, error) {
	var out FriendRequest
	return &out, f.c.post(ctx, "/v1/friends/request",
		map[string]any{"target_player_id": targetPlayerID}, &out)
}

// Accept accepts an incoming request.
func (f *FriendsAPI) Accept(ctx context.Context, requestID string) error {
	return f.c.post(ctx, "/v1/friends/accept", map[string]any{"request_id": requestID}, nil)
}

// Decline rejects an incoming request, or withdraws one you sent.
func (f *FriendsAPI) Decline(ctx context.Context, requestID string) error {
	return f.c.post(ctx, "/v1/friends/decline", map[string]any{"request_id": requestID}, nil)
}

// List returns friends with online status (30s accuracy, from the WebSocket
// gateway's heartbeat).
func (f *FriendsAPI) List(ctx context.Context) ([]Friend, error) {
	var out struct {
		Friends []Friend `json:"friends"`
	}
	return out.Friends, f.c.get(ctx, "/v1/friends", &out)
}

// Requests returns pending friend requests in both directions — the server
// sends one list, not two. Compare FromID against your own player id to tell
// an invitation you received from one you sent.
func (f *FriendsAPI) Requests(ctx context.Context) ([]FriendRequest, error) {
	var out struct {
		Requests []FriendRequest `json:"requests"`
	}
	return out.Requests, f.c.get(ctx, "/v1/friends/requests", &out)
}

// Remove deletes a friendship. It takes effect on both sides.
func (f *FriendsAPI) Remove(ctx context.Context, playerID string) error {
	return f.c.del(ctx, "/v1/friends/"+esc(playerID), nil)
}

// Block prevents someone from sending requests and hides room messages from
// them.
func (f *FriendsAPI) Block(ctx context.Context, targetPlayerID string) error {
	return f.c.post(ctx, "/v1/friends/block",
		map[string]any{"target_player_id": targetPlayerID}, nil)
}

// ---------- currency ----------

// CurrencyAPI reads balances and spends. Granting is server-side only, so a
// client can never mint currency.
type CurrencyAPI struct{ c *Client }

// Balances returns every currency this player holds.
func (cu *CurrencyAPI) Balances(ctx context.Context) ([]Balance, error) {
	var out struct {
		Balances []Balance `json:"balances"`
	}
	return out.Balances, cu.c.get(ctx, "/v1/currency", &out)
}

// Ledger reads this player's transaction history for one currency.
func (cu *CurrencyAPI) Ledger(ctx context.Context, currencyID string, limit, offset int) ([]Ledger, error) {
	var out struct {
		Entries []Ledger `json:"entries"`
	}
	q := query{"limit": itoa(limit), "offset": itoa(offset)}
	return out.Entries, cu.c.get(ctx, "/v1/currency/"+esc(currencyID)+"/ledger"+q.encode(), &out)
}

// Spend deducts an amount atomically.
//
// idempotencyKey must be unique per business transaction: retrying the same
// key never double-charges (the result reports Duplicate) — which is what
// makes a spend safe to retry after a dropped connection. Reusing one key
// with a different amount is rejected with 409 "idempotency_conflict".
// A short balance returns 402 "insufficient_funds".
func (cu *CurrencyAPI) Spend(ctx context.Context, currencyID string, amount int64, idempotencyKey, note string) (*Spend, error) {
	var out Spend
	return &out, cu.c.post(ctx, "/v1/currency/"+esc(currencyID)+"/spend", map[string]any{
		"amount": amount, "idempotency_key": idempotencyKey, "note": note,
	}, &out)
}

// ---------- mailbox ----------

// MailboxAPI reads the inbox and claims attachments.
type MailboxAPI struct{ c *Client }

// Inbox lists mail and the unread count for a badge. unreadOnly skips what
// the player has already read.
func (m *MailboxAPI) Inbox(ctx context.Context, unreadOnly bool) (mail []Mail, unread int, err error) {
	var out struct {
		Mail   []Mail `json:"mail"`
		Unread int    `json:"unread"`
	}
	q := query{}
	if unreadOnly {
		q["unread_only"] = "true"
	}
	err = m.c.get(ctx, "/v1/mailbox"+q.encode(), &out)
	return out.Mail, out.Unread, err
}

// Read marks one mail read, clearing it from the unread count.
func (m *MailboxAPI) Read(ctx context.Context, mailID string) error {
	return m.c.post(ctx, "/v1/mailbox/"+esc(mailID)+"/read", nil, nil)
}

// Claim collects a mail's attachments. Granting is atomic and one-shot:
// claiming twice returns 409 rather than paying out again.
func (m *MailboxAPI) Claim(ctx context.Context, mailID string) (*Claim, error) {
	var out Claim
	return &out, m.c.post(ctx, "/v1/mailbox/"+esc(mailID)+"/claim", nil, &out)
}

// ---------- announcements ----------

// AnnounceAPI polls server announcements. These need no token, so a client
// can show a maintenance notice before anyone logs in.
type AnnounceAPI struct{ c *Client }

// AnnounceOptions narrows a poll. AfterID fetches only what is newer than the
// last announcement already shown, which is how a client avoids re-reading
// the whole list. Platform and Channel filter targeted notices.
type AnnounceOptions struct {
	AfterID  string
	Platform string
	Channel  string
}

// List fetches active announcements.
func (a *AnnounceAPI) List(ctx context.Context, opts AnnounceOptions) ([]Announcement, error) {
	var out struct {
		Announcements []Announcement `json:"announcements"`
	}
	q := query{"after_id": opts.AfterID, "platform": opts.Platform, "channel": opts.Channel}
	path := "/v1/apps/" + esc(a.c.appID) + "/announcements" + q.encode()
	return out.Announcements, a.c.public(ctx, http.MethodGet, path, nil, &out)
}

// ---------- cloud functions ----------

// FunctionsAPI calls server-side JS functions.
type FunctionsAPI struct{ c *Client }

// Call invokes a cloud function with the player's token. Pass nil body for a
// function that takes no arguments.
func (f *FunctionsAPI) Call(ctx context.Context, name string, body any) (*FuncResult, error) {
	var out FuncResult
	return &out, f.c.post(ctx, "/v1/functions/"+esc(name), body, &out)
}

// CallPublic invokes a function marked public, without a token — for things
// that run before login, like a server-status check.
func (f *FunctionsAPI) CallPublic(ctx context.Context, name string, body any) (*FuncResult, error) {
	var out FuncResult
	path := "/v1/apps/" + esc(f.c.appID) + "/functions/" + esc(name)
	return &out, f.c.public(ctx, http.MethodPost, path, body, &out)
}

// ---------- updates ----------

// UpdatesAPI checks for new builds. These endpoints take no token because the
// updater runs before the game logs in.
type UpdatesAPI struct{ c *Client }

// CheckOptions identifies the running build. Platform and Arch default
// server-side to "any" when empty; DeviceID makes staged rollout decisions
// stable for one machine, so a player does not flip in and out of a rollout.
type CheckOptions struct {
	Version  string // currently installed version, e.g. "1.0.0"
	Platform string // windows | macos | linux | android | ios | web | any
	Arch     string // amd64 | arm64 | any
	Channel  string // defaults to stable
	DeviceID string
}

// Check asks whether a newer build exists. Always verify Artifact.SHA256
// after downloading and before installing.
func (u *UpdatesAPI) Check(ctx context.Context, opts CheckOptions) (*Update, error) {
	var out Update
	q := query{
		"version": opts.Version, "platform": opts.Platform, "arch": opts.Arch,
		"channel": opts.Channel, "device_id": opts.DeviceID,
	}
	path := "/v1/apps/" + esc(u.c.appID) + "/updates/check" + q.encode()
	return &out, u.c.public(ctx, http.MethodGet, path, nil, &out)
}

// Releases returns the published version history, for a changelog screen.
func (u *UpdatesAPI) Releases(ctx context.Context) ([]Release, error) {
	var out struct {
		Releases []Release `json:"releases"`
	}
	path := "/v1/apps/" + esc(u.c.appID) + "/releases"
	return out.Releases, u.c.public(ctx, http.MethodGet, path, nil, &out)
}

// ---------- client logs ----------

// LogsAPI ships client-side logs to the server for crash triage.
type LogsAPI struct{ c *Client }

// Report sends one log line. It is rate-limited server-side, so a crash loop
// cannot flood the log store.
func (l *LogsAPI) Report(ctx context.Context, entry Log) error {
	return l.c.post(ctx, "/v1/logs", entry, nil)
}

// ---------- dialogues ----------

// DialoguesAPI reads story scripts authored in the console. Read-only: the
// console owns the content.
type DialoguesAPI struct{ c *Client }

// List returns script keys and titles without their bodies.
func (d *DialoguesAPI) List(ctx context.Context) ([]Dialogue, error) {
	var out struct {
		Scripts []Dialogue `json:"scripts"`
	}
	return out.Scripts, d.c.get(ctx, "/v1/dialogues", &out)
}

// Get fetches one script including its content.
func (d *DialoguesAPI) Get(ctx context.Context, key string) (*Dialogue, error) {
	var out Dialogue
	return &out, d.c.get(ctx, "/v1/dialogues/"+esc(key), &out)
}

// ---------- chat history ----------

// ChatAPI reads persisted chat. Sending happens over the WebSocket, not here.
type ChatAPI struct{ c *Client }

// History reads a channel's backlog. BeforeID pages backwards through it,
// which is what a chat window does as the player scrolls up.
func (ch *ChatAPI) History(ctx context.Context, channel string, beforeID int64, limit int) ([]ChatMessage, error) {
	var out struct {
		Messages []ChatMessage `json:"messages"`
	}
	q := query{"channel": channel, "limit": itoa(limit)}
	if beforeID > 0 {
		q["before_id"] = itoa(int(beforeID))
	}
	return out.Messages, ch.c.get(ctx, "/v1/chat/history"+q.encode(), &out)
}

// ---------- game-level KV ----------

// KVAPI reads game-level config: activity switches, global thresholds. Reads
// are limited to the "public" namespace and "public_" prefixed ones; writing
// needs the app_secret and so belongs on a game server, not in a client —
// see Signer in signer.go.
type KVAPI struct{ c *Client }

// Get reads one config value into v, which must be a pointer.
func (k *KVAPI) Get(ctx context.Context, namespace, key string, v any) error {
	var out KV
	if err := k.c.get(ctx, "/v1/kv/"+esc(namespace)+"/"+esc(key), &out); err != nil {
		return err
	}
	return json.Unmarshal(out.Value, v)
}

// List returns the keys in a namespace, without their values.
func (k *KVAPI) List(ctx context.Context, namespace string, limit, offset int) ([]KVKey, error) {
	var out struct {
		Keys []KVKey `json:"keys"`
	}
	q := query{"limit": itoa(limit), "offset": itoa(offset)}
	return out.Keys, k.c.get(ctx, "/v1/kv/"+esc(namespace)+q.encode(), &out)
}
