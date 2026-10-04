// Package funcs runs developer-written server-side JavaScript (cloud
// functions) in a goja sandbox: per-call VM, hard timeout, and a small `mc`
// API surface for game data, player data and leaderboards.
package funcs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"

	"zekumo/internal/currency"
	"zekumo/internal/leaderboard"
	"zekumo/internal/repo"
)

const (
	execTimeout = 10 * time.Second // wall clock, including outbound fetches
	maxLogs     = 100
	maxLogLine  = 2000
)

type Runtime struct {
	GameData    repo.GameData
	PlayerData  repo.PlayerData
	Players     repo.Players
	Games       repo.Games
	Leaderboard *leaderboard.Service
	Currency    *currency.Service

	// AllowPrivateHTTP disables the private-address SSRF guard for
	// mc.http.fetch — local development only.
	AllowPrivateHTTP bool

	// Logs receives storage-layer failures that are hidden from script output.
	Logs LogSink

	guardOnce sync.Once
	guard     *guard
}

// HeapLimitBytes is the process heap ceiling above which running scripts are
// interrupted.
var HeapLimitBytes uint64 = 512 << 20

func (rt *Runtime) limits() *guard {
	rt.guardOnce.Do(func() { rt.guard = newGuard(HeapLimitBytes) })
	return rt.guard
}

// Request is what the script sees as the global `request`.
type Request struct {
	Method string         `json:"method"`
	Body   any            `json:"body"`
	Query  map[string]string `json:"query"`
	Player any            `json:"player"` // {id, nickname} or nil
}

type Result struct {
	Result any      `json:"result"`
	Logs   []string `json:"logs"`
}

// Execute runs one cloud function invocation. Script errors come back as
// error; the value of the script's final expression becomes Result.Result.
func (rt *Runtime) Execute(ctx context.Context, gameID string, fn *repo.CloudFunction, req Request) (*Result, error) {
	limits := rt.limits()
	if err := limits.acquire(); err != nil {
		return nil, err
	}
	defer limits.release()

	// Every blocking operation the script can start shares one deadline, so
	// the invocation cannot outlive its budget. vm.Interrupt alone cannot do
	// this: it only takes effect at the next bytecode boundary, never during
	// an in-flight DB query or fetch.
	deadline := time.Now().Add(execTimeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	defer limits.watch(vm)()

	timer := time.AfterFunc(execTimeout, func() { vm.Interrupt("execution timed out") })
	defer timer.Stop()

	logs := []string{}
	logFn := func(call goja.FunctionCall) goja.Value {
		if len(logs) < maxLogs {
			line := ""
			for i, arg := range call.Arguments {
				if i > 0 {
					line += " "
				}
				line += fmt.Sprintf("%v", arg.Export())
				if len(line) > maxLogLine { // a capped count is no use without a capped size
					line = line[:maxLogLine] + "…(truncated)"
					break
				}
			}
			logs = append(logs, line)
		}
		return goja.Undefined()
	}
	console := vm.NewObject()
	_ = console.Set("log", logFn)
	_ = console.Set("error", logFn)
	_ = vm.Set("console", console)
	_ = vm.Set("request", req)

	throw := func(err error) {
		panic(vm.ToValue(err.Error()))
	}
	// throwInternal keeps storage-layer detail (table, column and constraint
	// names from pgx) out of the response — /v1/apps/{app_id}/functions/{name}
	// is reachable without a token.
	throwInternal := func(err error) {
		if rt.Logs != nil {
			rt.Logs.Write(gameID, "error", "funcs", fn.Name, "storage error: "+err.Error(), nil)
		}
		panic(vm.ToValue("internal error"))
	}
	toJS := func(raw json.RawMessage) any {
		var v any
		_ = json.Unmarshal(raw, &v)
		return v
	}
	fromJS := func(v goja.Value) json.RawMessage {
		raw, err := json.Marshal(v.Export())
		if err != nil {
			throw(fmt.Errorf("value is not JSON-serializable: %w", err))
		}
		return raw
	}
	// requirePlayer ensures cross-player operations stay inside this game.
	requirePlayer := func(playerID string) *repo.Player {
		p, err := rt.Players.ByID(ctx, playerID)
		if err != nil || p == nil || p.GameID != gameID {
			throw(errors.New("no such player in this game: " + playerID))
		}
		return p
	}

	kv := vm.NewObject()
	_ = kv.Set("get", func(key string) any {
		raw, err := rt.GameData.Get(ctx, gameID, key)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			throwInternal(err)
		}
		return toJS(raw)
	})
	_ = kv.Set("set", func(key string, value goja.Value) {
		if err := rt.GameData.Put(ctx, gameID, key, fromJS(value)); err != nil {
			throwInternal(err)
		}
	})
	_ = kv.Set("del", func(key string) {
		if err := rt.GameData.Delete(ctx, gameID, key); err != nil {
			throwInternal(err)
		}
	})

	playerdata := vm.NewObject()
	_ = playerdata.Set("get", func(playerID, key string) any {
		requirePlayer(playerID)
		entry, err := rt.PlayerData.Get(ctx, playerID, key)
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		if err != nil {
			throwInternal(err)
		}
		return toJS(entry.Value)
	})
	_ = playerdata.Set("set", func(playerID, key string, value goja.Value) {
		requirePlayer(playerID)
		if _, err := rt.PlayerData.Put(ctx, playerID, key, fromJS(value)); err != nil {
			throwInternal(err)
		}
	})

	players := vm.NewObject()
	_ = players.Set("get", func(playerID string) any {
		p := requirePlayer(playerID)
		return map[string]any{"id": p.ID, "nickname": p.Nickname, "profile": toJS(p.Profile)}
	})

	lb := vm.NewObject()
	_ = lb.Set("submit", func(board, playerID string, score int64, rest ...string) int64 {
		requirePlayer(playerID)
		mode := ""
		if len(rest) > 0 {
			mode = rest[0]
		}
		newScore, err := rt.Leaderboard.Submit(ctx, gameID, board, playerID, score, mode)
		if err != nil {
			throwInternal(err)
		}
		return newScore
	})
	_ = lb.Set("top", func(board string, limit int64) any {
		if limit <= 0 || limit > 100 {
			limit = 10
		}
		entries, err := rt.Leaderboard.Top(ctx, gameID, board, 0, limit)
		if err != nil {
			throwInternal(err)
		}
		return entries
	})

	httpObj := vm.NewObject()
	fetchCalls := 0
	allowlist, allowlistLoaded := "", false
	_ = httpObj.Set("fetch", func(rawURL string, rest ...fetchOptions) any {
		fetchCalls++
		if fetchCalls > maxFetchCalls {
			throw(fmt.Errorf("at most %d outbound requests per invocation", maxFetchCalls))
		}
		if !allowlistLoaded { // lazy: only invocations that fetch pay the lookup
			game, err := rt.Games.ByID(ctx, gameID)
			if err != nil {
				throwInternal(err)
			}
			allowlist, allowlistLoaded = game.FuncHTTPAllowlist, true
		}
		opts := fetchOptions{}
		if len(rest) > 0 {
			opts = rest[0]
		}
		res, err := rt.doFetch(allowlist, rawURL, opts, time.Until(deadline))
		if err != nil {
			throw(err)
		}
		var parsed any
		if json.Unmarshal([]byte(res.Body), &parsed) == nil {
			res.JSON = parsed
		}
		return res
	})

	currencyObj := vm.NewObject()
	_ = currencyObj.Set("grant", func(playerID, name string, amount int64, idemKey string, rest ...string) any {
		if rt.Currency == nil {
			throw(errors.New("currency API is not available"))
		}
		if amount <= 0 {
			throw(errors.New("amount must be positive"))
		}
		if idemKey == "" {
			throw(errors.New("idempotency key is required"))
		}
		note := ""
		if len(rest) > 0 {
			note = rest[0]
		}
		balance, duplicate, err := rt.Currency.GrantByName(ctx, gameID, name, playerID, amount, idemKey, note)
		if errors.Is(err, repo.ErrNotFound) {
			throw(errors.New("no such currency or player in this game"))
		}
		if errors.Is(err, repo.ErrIdemConflict) {
			throw(errors.New("idempotency key was already used with a different amount"))
		}
		if err != nil {
			throwInternal(err)
		}
		return map[string]any{"balance": balance, "duplicate": duplicate}
	})

	mc := vm.NewObject()
	_ = mc.Set("kv", kv)
	_ = mc.Set("currency", currencyObj)
	_ = mc.Set("playerdata", playerdata)
	_ = mc.Set("players", players)
	_ = mc.Set("leaderboard", lb)
	_ = mc.Set("http", httpObj)
	_ = vm.Set("mc", mc)

	value, err := vm.RunScript(fn.Name, fn.Code)
	if err != nil {
		var exc *goja.Exception
		if errors.As(err, &exc) {
			return nil, fmt.Errorf("script threw: %v", exc.Value())
		}
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			return nil, errors.New("script exceeded the 10s time limit")
		}
		return nil, err
	}
	res := &Result{Logs: logs}
	if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		res.Result = value.Export()
	}
	return res, nil
}
