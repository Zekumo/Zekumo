package server

import (
	"io/fs"
	"log"
	"net/http"
	"time"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/ratelimit"
	"minicloud/internal/storage"
	"minicloud/web"
)

// Per-IP limits are coarse flood control only, deliberately generous: carrier
// CGNAT and campus networks put many legitimate players behind one address,
// so a tight per-IP cap locks out real users. The actual brute-force defense
// is the per-identity lockout (auth.AttemptLimiter), which is unaffected by
// how many addresses an attacker spreads across.
var (
	authRule      = ratelimit.Rule{Name: "auth", Limit: 120, Window: time.Minute}
	anonRule      = ratelimit.Rule{Name: "anon", Limit: 120, Window: time.Minute}
	adminRule     = ratelimit.Rule{Name: "admin", Limit: 20, Window: time.Minute}
	fnAnonRule    = ratelimit.Rule{Name: "fn-anon", Limit: 120, Window: time.Minute}
	fnRule        = ratelimit.Rule{Name: "fn", Limit: 120, Window: time.Minute}
	clientLogRule = ratelimit.Rule{Name: "logs", Limit: 60, Window: time.Minute}
)

// router bundles the mux with the helpers that attach auth and rate limiting,
// so each route reads as one line.
type router struct {
	*http.ServeMux
	d *deps
}

// anon registers a route reachable without a token, throttled per caller address.
func (rt router) anon(pattern string, rule ratelimit.Rule, fn http.HandlerFunc) {
	rt.Handle(pattern, rt.d.limiter.ByIP(rule, fn))
}

// role registers a route requiring a token of the given role.
func (rt router) role(role, pattern string, fn http.HandlerFunc) {
	rt.Handle(pattern, rt.d.authH.Middleware(role, fn))
}

// player registers a player route, additionally throttled per player so one
// noisy client cannot crowd out the others.
func (rt router) player(pattern string, rule ratelimit.Rule, fn http.HandlerFunc) {
	byPlayer := func(r *http.Request) string {
		if c := auth.ClaimsFrom(r.Context()); c != nil {
			return c.Subject
		}
		return rt.d.limiter.ClientIP(r)
	}
	rt.Handle(pattern, rt.d.authH.Middleware(auth.RolePlayer,
		rt.d.limiter.Middleware(rule, byPlayer, fn)))
}

func routes(d *deps) *http.ServeMux {
	rt := router{ServeMux: http.NewServeMux(), d: d}
	// Liveness: the process is up. Deliberately cheap and dependency-free —
	// restarting on a database blip would turn an outage into a crash loop.
	rt.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Readiness: this instance can actually serve. A proxy that only polls
	// /healthz keeps sending traffic to an instance whose database is gone.
	rt.HandleFunc("GET /readyz", d.readyz)
	registerAuth(rt)
	registerPlayer(rt)
	registerAdmin(rt)
	registerPages(rt)
	return rt.ServeMux
}

// registerAuth covers game login, platform accounts and OAuth2.
func registerAuth(rt router) {
	d := rt.d
	rt.anon("POST /v1/auth/login", authRule, d.authH.Login)
	rt.anon("POST /v1/auth/register", authRule, d.authH.Register)

	rt.anon("POST /sso/api/register", authRule, d.ssoH.Register)
	rt.anon("POST /sso/api/login", authRule, d.ssoH.Login)
	rt.HandleFunc("GET /sso/api/authorize/info", d.ssoH.AuthorizeInfo)
	rt.role(auth.RoleAccount, "GET /sso/api/me", d.ssoH.Me)
	rt.role(auth.RoleAccount, "POST /sso/api/tickets", d.ssoH.IssueTicket)
	rt.role(auth.RoleAccount, "GET /sso/api/authorizations", d.oauthH.Authorizations)
	rt.role(auth.RoleAccount, "DELETE /sso/api/authorizations/{client_id}", d.oauthH.RevokeAuthorization)

	rt.HandleFunc("GET /oauth/api/info", d.oauthH.Info)
	rt.role(auth.RoleAccount, "POST /oauth/api/approve", d.oauthH.Approve)
	rt.anon("POST /oauth/token", anonRule, d.oauthH.Token)
	rt.role(auth.RoleOAuth, "GET /oauth/userinfo", d.oauthH.Userinfo)
}

// registerPlayer covers the in-game APIs plus the token-free endpoints a
// launcher needs before anyone has logged in.
func registerPlayer(rt router) {
	d := rt.d
	rt.HandleFunc("GET /v1/apps/{app_id}/updates/check", d.updatesH.CheckUpdate)
	rt.HandleFunc("GET /v1/apps/{app_id}/releases", d.updatesH.PublicReleases)
	rt.HandleFunc("GET /v1/apps/{app_id}/announcements", d.announcementsH.PublicList)
	rt.anon("GET /v1/apps/{app_id}/functions/{name}", fnAnonRule, d.fnH.PublicCall)
	rt.anon("POST /v1/apps/{app_id}/functions/{name}", fnAnonRule, d.fnH.PublicCall)

	p := func(pattern string, fn http.HandlerFunc) { rt.role(auth.RolePlayer, pattern, fn) }
	p("GET /v1/player/profile", d.playerH.GetProfile)
	p("PUT /v1/player/profile", d.playerH.UpdateProfile)
	p("GET /v1/player/data", d.playerH.ListData)
	p("GET /v1/player/data/{key}", d.playerH.GetData)
	p("PUT /v1/player/data/{key}", d.playerH.PutData)
	p("DELETE /v1/player/data/{key}", d.playerH.DeleteData)
	p("POST /v1/player/bind", d.ssoH.BindPlayer)
	p("POST /v1/leaderboards/{board}/score", d.lbH.SubmitScore)
	p("GET /v1/leaderboards/{board}", d.lbH.Top)
	p("GET /v1/leaderboards/{board}/me", d.lbH.Me)
	p("GET /v1/dialogues", d.dialogueH.List)
	p("GET /v1/dialogues/{key}", d.dialogueH.Get)
	p("GET /v1/chat/history", d.chatH.History)
	p("GET /v1/ws", d.hub.ServeWS)
	// Friends
	p("POST /v1/friends/request", d.friendsH.SendRequest)
	p("POST /v1/friends/accept", d.friendsH.Accept)
	p("POST /v1/friends/decline", d.friendsH.Decline)
	p("GET /v1/friends", d.friendsH.List)
	p("GET /v1/friends/requests", d.friendsH.ListRequests)
	p("DELETE /v1/friends/{player_id}", d.friendsH.Remove)
	p("POST /v1/friends/block", d.friendsH.Block)
	// Achievements
	p("GET /v1/achievements", d.achievementsH.List)
	p("GET /v1/achievements/unlocked", d.achievementsH.Unlocked)
	// Currency
	p("GET /v1/currency", d.currencyH.MyBalances)
	p("GET /v1/currency/{cid}/ledger", d.currencyH.MyLedger)
	p("POST /v1/currency/{cid}/spend", d.currencyH.Spend)
	// Mailbox
	p("GET /v1/mailbox", d.mailH.Inbox)
	p("POST /v1/mailbox/{mid}/read", d.mailH.Read)
	p("POST /v1/mailbox/{mid}/claim", d.mailH.Claim)
	// Game-level KV: players read public namespaces, servers write with an
	// app_secret signature (no player token).
	p("GET /v1/kv/{namespace}", d.kvH.List)
	p("GET /v1/kv/{namespace}/{key}", d.kvH.Get)
	rt.anon("PUT /v1/kv/{namespace}/{key}", anonRule, d.kvH.Put)
	rt.anon("DELETE /v1/kv/{namespace}/{key}", anonRule, d.kvH.Delete)

	rt.player("POST /v1/logs", clientLogRule, d.logsH.ClientReport)
	rt.player("GET /v1/functions/{name}", fnRule, d.fnH.Call)
	rt.player("POST /v1/functions/{name}", fnRule, d.fnH.Call)
}

// registerAdmin covers the developer console API.
func registerAdmin(rt router) {
	d := rt.d
	rt.anon("POST /admin/api/login", adminRule, d.adminH.Login)
	a := func(pattern string, fn http.HandlerFunc) { rt.role(auth.RoleAdmin, pattern, fn) }

	a("GET /admin/api/me", d.adminH.Me)
	a("GET /admin/api/games", d.adminH.ListGames)
	a("POST /admin/api/games", d.adminH.CreateGame)
	a("DELETE /admin/api/games/{id}", d.adminH.DeleteGame)
	a("GET /admin/api/games/{id}/players", d.adminH.ListPlayers)
	a("GET /admin/api/players/{pid}/data", d.adminH.PlayerData)
	a("GET /admin/api/accounts", d.adminH.ListAccounts)
	a("DELETE /admin/api/accounts/{id}", d.adminH.DeleteAccount)
	a("PUT /admin/api/games/{id}/sso", d.adminH.UpdateGameSSO)
	a("PUT /admin/api/games/{id}/http-allowlist", d.adminH.UpdateGameHTTPAllowlist)

	a("GET /admin/api/oauth/clients", d.adminH.ListOAuthClients)
	a("POST /admin/api/oauth/clients", d.adminH.CreateOAuthClient)
	a("PUT /admin/api/oauth/clients/{client_id}", d.adminH.UpdateOAuthClient)
	a("DELETE /admin/api/oauth/clients/{client_id}", d.adminH.DeleteOAuthClient)

	a("GET /admin/api/games/{id}/dialogues", d.adminH.ListDialogues)
	a("GET /admin/api/games/{id}/dialogues/{key}", d.adminH.GetDialogue)
	a("PUT /admin/api/games/{id}/dialogues/{key}", d.adminH.PutDialogue)
	a("DELETE /admin/api/games/{id}/dialogues/{key}", d.adminH.DeleteDialogue)

	a("GET /admin/api/games/{id}/functions", d.fnH.List)
	a("GET /admin/api/games/{id}/functions/{name}", d.fnH.Get)
	a("PUT /admin/api/games/{id}/functions/{name}", d.fnH.Put)
	a("DELETE /admin/api/games/{id}/functions/{name}", d.fnH.Delete)
	a("POST /admin/api/games/{id}/functions/{name}/test", d.fnH.Test)

	a("GET /admin/api/games/{id}/webhooks", d.hooksH.List)
	a("POST /admin/api/games/{id}/webhooks", d.hooksH.Create)
	a("PUT /admin/api/webhooks/{wid}", d.hooksH.Update)
	a("DELETE /admin/api/webhooks/{wid}", d.hooksH.Delete)
	a("GET /admin/api/webhooks/{wid}/deliveries", d.hooksH.Deliveries)
	a("POST /admin/api/webhooks/{wid}/test", d.hooksH.Test)

	a("GET /admin/api/stats", d.statsH.PlatformStats)
	a("GET /admin/api/health", d.healthH.Health)
	a("GET /admin/api/games/{id}/leaderboards", d.lbH.AdminBoards)
	a("GET /admin/api/games/{id}/leaderboards/{board}", d.lbH.AdminTop)
	a("GET /admin/api/games/{id}/stats", d.statsH.GameStats)
	a("GET /admin/api/logs", d.logsH.AdminQuery)

	// Release management (update distribution).
	a("GET /admin/api/games/{id}/releases", d.updatesH.ListReleases)
	a("POST /admin/api/games/{id}/releases", d.updatesH.CreateRelease)
	a("DELETE /admin/api/games/{id}/releases/{version}", d.updatesH.DeleteRelease)
	a("GET /admin/api/games/{id}/releases/{version}/artifacts", d.updatesH.ListArtifacts)
	a("POST /admin/api/games/{id}/releases/{version}/artifacts", d.updatesH.CreateArtifact)
	a("POST /admin/api/games/{id}/releases/{version}/artifacts/{aid}/complete", d.updatesH.CompleteArtifact)
	a("POST /admin/api/games/{id}/releases/{version}/publish", d.updatesH.Publish)
	a("POST /admin/api/games/{id}/releases/{version}/rollout", d.updatesH.Rollout)
	a("POST /admin/api/games/{id}/releases/{version}/revoke", d.updatesH.Revoke)

	// Achievements
	a("GET /admin/api/games/{id}/achievements", d.achievementsH.AdminList)
	a("POST /admin/api/games/{id}/achievements", d.achievementsH.AdminCreate)
	a("PUT /admin/api/achievements/{aid}", d.achievementsH.AdminUpdate)
	a("DELETE /admin/api/achievements/{aid}", d.achievementsH.AdminDelete)
	a("POST /admin/api/achievements/{aid}/unlock", d.achievementsH.AdminUnlock)

	// Announcements
	a("GET /admin/api/games/{id}/announcements", d.announcementsH.AdminList)
	a("POST /admin/api/games/{id}/announcements", d.announcementsH.AdminCreate)
	a("PUT /admin/api/announcements/{aid}", d.announcementsH.AdminUpdate)
	a("DELETE /admin/api/announcements/{aid}", d.announcementsH.AdminDeactivate)

	// Currency
	a("GET /admin/api/games/{id}/currencies", d.currencyH.AdminList)
	a("POST /admin/api/games/{id}/currencies", d.currencyH.AdminCreate)
	a("PUT /admin/api/currencies/{cid}", d.currencyH.AdminUpdate)
	a("DELETE /admin/api/currencies/{cid}", d.currencyH.AdminDelete)
	a("POST /admin/api/games/{id}/currency/{cid}/grant", d.currencyH.AdminGrant)
	a("GET /admin/api/players/{pid}/currency", d.currencyH.AdminPlayerBalances)
	a("GET /admin/api/players/{pid}/currency/{cid}/ledger", d.currencyH.AdminPlayerLedger)

	// Mail
	a("POST /admin/api/games/{id}/mail", d.mailH.AdminSend)
	a("GET /admin/api/games/{id}/mail", d.mailH.AdminList)

	// Retention stats
	a("GET /admin/api/games/{id}/stats/retention", d.statsH.Retention)
	a("GET /admin/api/games/{id}/stats/funnel", d.statsH.Funnel)

	// Bans
	a("POST /admin/api/players/{pid}/ban", d.bansH.Ban)
	a("POST /admin/api/players/{pid}/unban", d.bansH.Unban)
	a("GET /admin/api/games/{id}/bans", d.bansH.List)

	// Data exports
	a("POST /admin/api/games/{id}/exports", d.exportsH.Submit)
	a("GET /admin/api/exports/{job_id}", d.exportsH.Status)
	a("GET /admin/api/games/{id}/exports", d.exportsH.History)
}

// registerPages serves the embedded console and the hosted login pages.
func registerPages(rt router) {
	if local, ok := rt.d.blob.(*storage.Local); ok {
		// The local storage driver signs and serves artifact PUT/GET itself.
		rt.Handle("/storage/", http.StripPrefix("/storage", local.Handler()))
	}

	consoleFS, err := fs.Sub(web.FS, "admin")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	rt.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServer(http.FS(consoleFS))))
	rt.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	// Hosted pages games redirect players to.
	rt.HandleFunc("GET /sso/authorize", staticPage("sso/index.html"))
	rt.HandleFunc("GET /oauth/authorize", staticPage("oauth/index.html"))
}

func staticPage(name string) http.HandlerFunc {
	body, err := web.FS.ReadFile(name)
	if err != nil {
		log.Fatalf("embed %s: %v", name, err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}
}
