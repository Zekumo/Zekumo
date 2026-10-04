// client.go is the SDK's entry point and its only HTTP plumbing: build a
// Client, hand it credentials, and every API group hangs off it.
//
//	mc := zekumo.New(zekumo.Options{AppID: "zk_xxx", BaseURL: "https://api.example.com"})
//	if _, err := mc.Auth.LoginAsGuest(ctx, deviceID, "Player1"); err != nil { ... }
//	boards, err := mc.Leaderboards.Top(ctx, "weekly", zekumo.TopOptions{Limit: 10})
//
// The token lives here rather than in the caller: login stores it, every
// later request picks it up, and Logout clears it. A desktop game has a
// render thread and a network goroutine touching the same Client, so the
// token is mutex-guarded — reading a string while another goroutine assigns
// it is a data race Go will flag under -race.

package zekumo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultTimeout bounds a single request. A game that hangs for a minute on a
// leaderboard read looks broken, so the default fails fast enough to retry.
const defaultTimeout = 30 * time.Second

// Options configures a Client. AppID and BaseURL are required; the rest have
// working defaults.
type Options struct {
	AppID   string // from the console; safe to ship in a client binary
	BaseURL string // e.g. https://api.example.com, no trailing slash needed
	Token   string // optional: resume a saved session instead of logging in again

	// HTTPClient replaces the default client — use it to set a proxy, pin a
	// certificate, or share one connection pool across a game.
	HTTPClient *http.Client
}

// Client is the SDK handle. Build it with New and keep one for the process:
// it holds the session token and reuses HTTP connections.
type Client struct {
	appID   string
	baseURL string
	http    *http.Client

	mu    sync.RWMutex // guards token against concurrent login / request
	token string

	// API groups, mirroring the server's own grouping so a reader can map a
	// call back to the REST docs.
	Auth         *AuthAPI
	Player       *PlayerAPI
	Data         *DataAPI
	Leaderboards *LeaderboardsAPI
	Achievements *AchievementsAPI
	Friends      *FriendsAPI
	Currency     *CurrencyAPI
	Mailbox      *MailboxAPI
	Announce     *AnnounceAPI
	Functions    *FunctionsAPI
	Updates      *UpdatesAPI
	Logs         *LogsAPI
	Dialogues    *DialoguesAPI
	Chat         *ChatAPI
	KV           *KVAPI
}

// New builds a Client. It does no I/O, so it cannot fail — a bad BaseURL
// surfaces on the first call, where the error can actually be handled.
func New(opts Options) *Client {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	c := &Client{
		appID:   opts.AppID,
		baseURL: strings.TrimSuffix(opts.BaseURL, "/"), // callers write it both ways
		http:    httpClient,
		token:   opts.Token,
	}
	// Each group holds the same *Client, so a login through Auth is visible
	// to every other group immediately.
	c.Auth = &AuthAPI{c: c}
	c.Player = &PlayerAPI{c: c}
	c.Data = &DataAPI{c: c}
	c.Leaderboards = &LeaderboardsAPI{c: c}
	c.Achievements = &AchievementsAPI{c: c}
	c.Friends = &FriendsAPI{c: c}
	c.Currency = &CurrencyAPI{c: c}
	c.Mailbox = &MailboxAPI{c: c}
	c.Announce = &AnnounceAPI{c: c}
	c.Functions = &FunctionsAPI{c: c}
	c.Updates = &UpdatesAPI{c: c}
	c.Logs = &LogsAPI{c: c}
	c.Dialogues = &DialoguesAPI{c: c}
	c.Chat = &ChatAPI{c: c}
	c.KV = &KVAPI{c: c}
	return c
}

// Token returns the current session token, for persisting a session across
// restarts so the player is not asked to log in twice.
func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// SetToken installs a token directly, for resuming a saved session.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

// AppID returns the configured app id, which the realtime client and the
// public (token-free) endpoints need in their paths.
func (c *Client) AppID() string { return c.appID }

// BaseURL returns the configured base URL, used to derive the WebSocket URL.
func (c *Client) BaseURL() string { return c.baseURL }

// ---------- errors ----------

// Error is a structured API error. Code is the server's stable machine-
// readable string — branch on that, never on Message, which is prose and may
// be reworded.
//
//	var apiErr *zekumo.Error
//	if errors.As(err, &apiErr) && apiErr.Code == "insufficient_funds" {
//	    showShop()
//	}
type Error struct {
	Status  int    // HTTP status
	Code    string // e.g. insufficient_funds, player_banned
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("zekumo: %s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

// IsCode reports whether err is an API error with the given code. It saves
// every caller from writing the errors.As dance by hand.
func IsCode(err error, code string) bool {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == code
}

// ---------- request plumbing ----------

// query builds a query string, dropping empty values so the server sees
// absent rather than blank — the two mean different things for filters like
// platform and channel.
type query map[string]string

func (q query) encode() string {
	if len(q) == 0 {
		return ""
	}
	vals := url.Values{}
	for k, v := range q {
		if v != "" {
			vals.Set(k, v)
		}
	}
	if len(vals) == 0 {
		return ""
	}
	return "?" + vals.Encode()
}

// itoa keeps call sites readable: optional numeric params are "unset" when
// zero, and an unset param must not appear in the query at all.
func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// do performs one request and decodes the response into out (nil to discard).
// Auth mode is decided per call: authed requests carry the bearer token,
// public ones (update checks, announcements) deliberately do not.
func (c *Client) do(ctx context.Context, method, path string, body, out any, authed bool) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("zekumo: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("zekumo: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authed {
		if token := c.Token(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("zekumo: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		return decodeError(res)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("zekumo: decode %s %s: %w", method, path, err)
	}
	return nil
}

// decodeError turns an error response into *Error. The server always answers
// {"error":{"code","message"}}, but a proxy or gateway in front of it may
// return HTML or nothing at all, so the fallback still yields a usable Code.
func decodeError(res *http.Response) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10)) // cap: an HTML error page can be huge
	_ = json.Unmarshal(raw, &payload)

	code, message := payload.Error.Code, payload.Error.Message
	if code == "" {
		code = "http_" + strconv.Itoa(res.StatusCode) // e.g. http_502 from a gateway
	}
	if message == "" {
		message = strings.TrimSpace(string(raw))
		if message == "" {
			message = res.Status
		}
	}
	return &Error{Status: res.StatusCode, Code: code, Message: message}
}

// get/post/put/del are the authed shorthands the API groups call. Naming them
// after the verb keeps each call site one line and readable as a REST call.
func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out, true)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out, true)
}

func (c *Client) put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out, true)
}

func (c *Client) del(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodDelete, path, nil, out, true)
}

// public issues a request with no bearer token, for the endpoints that run
// before login: update checks (the updater runs first) and announcements.
func (c *Client) public(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, path, body, out, false)
}
