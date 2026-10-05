package server

import (
	"io/fs"
	"log"
	"net/http"
	"time"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/ratelimit"
	"zekumo/internal/storage"
	"zekumo/internal/tenant"
	"zekumo/web"
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
	ta := func(pattern, minRole string, resource tenant.Resource, fn http.HandlerFunc) {
		rt.Handle(pattern, d.authH.Middleware(auth.RoleAdmin,
			d.tenants.Require(minRole, resource, fn)))
	}

	// Identity and workspace discovery are intentionally header-exempt.
	a("GET /admin/api/me", d.adminH.Me)
	a("GET /admin/api/workspaces", d.tenantH.ListWorkspaces)
	a("POST /admin/api/workspaces", d.tenantH.CreateWorkspace)
	ta("GET /admin/api/workspaces/{workspace_id}/members", "viewer", tenant.ResourceWorkspace, d.tenantH.ListMembers)
	ta("POST /admin/api/workspaces/{workspace_id}/members", "admin", tenant.ResourceWorkspace, d.tenantH.AddMember)
	ta("PUT /admin/api/workspaces/{workspace_id}/members/{user_id}", "admin", tenant.ResourceWorkspace, d.tenantH.UpdateMember)
	ta("DELETE /admin/api/workspaces/{workspace_id}/members/{user_id}", "admin", tenant.ResourceWorkspace, d.tenantH.RemoveMember)

	ta("GET /admin/api/games", "viewer", tenant.ResourceNone, d.adminH.ListGames)
	ta("POST /admin/api/games", "admin", tenant.ResourceNone, d.adminH.CreateGame)
	ta("DELETE /admin/api/games/{id}", "owner", tenant.ResourceGame, d.adminH.DeleteGame)
	ta("GET /admin/api/games/{id}/players", "viewer", tenant.ResourceGame, d.adminH.ListPlayers)
	ta("GET /admin/api/players/{pid}/data", "viewer", tenant.ResourcePlayer, d.adminH.PlayerData)
	ta("GET /admin/api/accounts", "viewer", tenant.ResourceNone, d.adminH.ListAccounts)
	ta("DELETE /admin/api/accounts/{id}", "owner", tenant.ResourceAccount, d.adminH.DeleteAccount)
	ta("PUT /admin/api/games/{id}/sso", "editor", tenant.ResourceGame, d.adminH.UpdateGameSSO)
	ta("PUT /admin/api/games/{id}/http-allowlist", "editor", tenant.ResourceGame, d.adminH.UpdateGameHTTPAllowlist)
	ta("PUT /admin/api/games/{id}/friend-limit", "editor", tenant.ResourceGame, d.adminH.UpdateGameFriendLimit)

	ta("GET /admin/api/oauth/clients", "viewer", tenant.ResourceNone, d.adminH.ListOAuthClients)
	ta("POST /admin/api/oauth/clients", "admin", tenant.ResourceNone, d.adminH.CreateOAuthClient)
	ta("PUT /admin/api/oauth/clients/{client_id}", "editor", tenant.ResourceOAuthClient, d.adminH.UpdateOAuthClient)
	ta("DELETE /admin/api/oauth/clients/{client_id}", "admin", tenant.ResourceOAuthClient, d.adminH.DeleteOAuthClient)

	ta("GET /admin/api/games/{id}/dialogues", "viewer", tenant.ResourceGame, d.adminH.ListDialogues)
	ta("GET /admin/api/games/{id}/dialogues/{key}", "viewer", tenant.ResourceGame, d.adminH.GetDialogue)
	ta("PUT /admin/api/games/{id}/dialogues/{key}", "editor", tenant.ResourceGame, d.adminH.PutDialogue)
	ta("DELETE /admin/api/games/{id}/dialogues/{key}", "editor", tenant.ResourceGame, d.adminH.DeleteDialogue)

	ta("GET /admin/api/games/{id}/functions", "viewer", tenant.ResourceGame, d.fnH.List)
	ta("GET /admin/api/games/{id}/functions/{name}", "viewer", tenant.ResourceGame, d.fnH.Get)
	ta("PUT /admin/api/games/{id}/functions/{name}", "editor", tenant.ResourceGame, d.fnH.Put)
	ta("DELETE /admin/api/games/{id}/functions/{name}", "editor", tenant.ResourceGame, d.fnH.Delete)
	ta("POST /admin/api/games/{id}/functions/{name}/test", "editor", tenant.ResourceGame, d.fnH.Test)

	ta("GET /admin/api/games/{id}/webhooks", "viewer", tenant.ResourceGame, d.hooksH.List)
	ta("POST /admin/api/games/{id}/webhooks", "editor", tenant.ResourceGame, d.hooksH.Create)
	ta("PUT /admin/api/webhooks/{wid}", "editor", tenant.ResourceWebhook, d.hooksH.Update)
	ta("DELETE /admin/api/webhooks/{wid}", "editor", tenant.ResourceWebhook, d.hooksH.Delete)
	ta("GET /admin/api/webhooks/{wid}/deliveries", "viewer", tenant.ResourceWebhook, d.hooksH.Deliveries)
	ta("POST /admin/api/webhooks/{wid}/test", "editor", tenant.ResourceWebhook, d.hooksH.Test)

	ta("GET /admin/api/stats", "viewer", tenant.ResourceNone, d.statsH.PlatformStats)
	ta("GET /admin/api/health", "owner", tenant.ResourceNone, d.healthH.Health)
	ta("GET /admin/api/games/{id}/leaderboards", "viewer", tenant.ResourceGame, d.lbH.AdminBoards)
	ta("GET /admin/api/games/{id}/leaderboards/{board}", "viewer", tenant.ResourceGame, d.lbH.AdminTop)
	ta("GET /admin/api/games/{id}/stats", "viewer", tenant.ResourceGame, d.statsH.GameStats)
	ta("GET /admin/api/logs", "viewer", tenant.ResourceNone, d.logsH.AdminQuery)

	// Release management (update distribution).
	ta("GET /admin/api/games/{id}/releases", "viewer", tenant.ResourceGame, d.updatesH.ListReleases)
	ta("POST /admin/api/games/{id}/releases", "editor", tenant.ResourceGame, d.updatesH.CreateRelease)
	ta("DELETE /admin/api/games/{id}/releases/{version}", "editor", tenant.ResourceGame, d.updatesH.DeleteRelease)
	ta("GET /admin/api/games/{id}/releases/{version}/artifacts", "viewer", tenant.ResourceGame, d.updatesH.ListArtifacts)
	ta("POST /admin/api/games/{id}/releases/{version}/artifacts", "editor", tenant.ResourceGame, d.updatesH.CreateArtifact)
	ta("POST /admin/api/games/{id}/releases/{version}/artifacts/{aid}/complete", "editor", tenant.ResourceArtifact, d.updatesH.CompleteArtifact)
	ta("POST /admin/api/games/{id}/releases/{version}/publish", "editor", tenant.ResourceGame, d.updatesH.Publish)
	ta("POST /admin/api/games/{id}/releases/{version}/rollout", "editor", tenant.ResourceGame, d.updatesH.Rollout)
	ta("POST /admin/api/games/{id}/releases/{version}/revoke", "editor", tenant.ResourceGame, d.updatesH.Revoke)

	// Achievements
	ta("GET /admin/api/games/{id}/achievements", "viewer", tenant.ResourceGame, d.achievementsH.AdminList)
	ta("POST /admin/api/games/{id}/achievements", "editor", tenant.ResourceGame, d.achievementsH.AdminCreate)
	ta("PUT /admin/api/achievements/{aid}", "editor", tenant.ResourceAchievement, d.achievementsH.AdminUpdate)
	ta("DELETE /admin/api/achievements/{aid}", "editor", tenant.ResourceAchievement, d.achievementsH.AdminDelete)
	ta("POST /admin/api/achievements/{aid}/unlock", "editor", tenant.ResourceAchievement, d.achievementsH.AdminUnlock)

	// Announcements
	ta("GET /admin/api/games/{id}/announcements", "viewer", tenant.ResourceGame, d.announcementsH.AdminList)
	ta("POST /admin/api/games/{id}/announcements", "editor", tenant.ResourceGame, d.announcementsH.AdminCreate)
	ta("PUT /admin/api/announcements/{aid}", "editor", tenant.ResourceAnnouncement, d.announcementsH.AdminUpdate)
	ta("DELETE /admin/api/announcements/{aid}", "editor", tenant.ResourceAnnouncement, d.announcementsH.AdminDeactivate)

	// Currency
	ta("GET /admin/api/games/{id}/currencies", "viewer", tenant.ResourceGame, d.currencyH.AdminList)
	ta("POST /admin/api/games/{id}/currencies", "editor", tenant.ResourceGame, d.currencyH.AdminCreate)
	ta("PUT /admin/api/currencies/{cid}", "editor", tenant.ResourceCurrency, d.currencyH.AdminUpdate)
	ta("DELETE /admin/api/currencies/{cid}", "editor", tenant.ResourceCurrency, d.currencyH.AdminDelete)
	ta("POST /admin/api/games/{id}/currency/{cid}/grant", "editor", tenant.ResourceGameCurrency, d.currencyH.AdminGrant)
	ta("GET /admin/api/players/{pid}/currency", "viewer", tenant.ResourcePlayer, d.currencyH.AdminPlayerBalances)
	ta("GET /admin/api/players/{pid}/currency/{cid}/ledger", "viewer", tenant.ResourcePlayerCurrency, d.currencyH.AdminPlayerLedger)

	// Mail
	ta("POST /admin/api/games/{id}/mail", "editor", tenant.ResourceGame, d.mailH.AdminSend)
	ta("GET /admin/api/games/{id}/mail", "viewer", tenant.ResourceGame, d.mailH.AdminList)

	// Retention stats
	ta("GET /admin/api/games/{id}/stats/retention", "viewer", tenant.ResourceGame, d.statsH.Retention)
	ta("GET /admin/api/games/{id}/stats/funnel", "viewer", tenant.ResourceGame, d.statsH.Funnel)

	// Bans
	ta("POST /admin/api/players/{pid}/ban", "editor", tenant.ResourcePlayer, d.bansH.Ban)
	ta("POST /admin/api/players/{pid}/unban", "editor", tenant.ResourcePlayer, d.bansH.Unban)
	ta("GET /admin/api/games/{id}/bans", "viewer", tenant.ResourceGame, d.bansH.List)

	// Data exports
	ta("POST /admin/api/games/{id}/exports", "editor", tenant.ResourceGame, d.exportsH.Submit)
	ta("GET /admin/api/exports/{job_id}", "viewer", tenant.ResourceExport, d.exportsH.Status)
	ta("GET /admin/api/games/{id}/exports", "viewer", tenant.ResourceGame, d.exportsH.History)
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
	rt.HandleFunc("GET /{$}", staticPage("site/index.html"))
	siteFS, err := fs.Sub(web.FS, "site")
	if err != nil {
		log.Fatalf("embed site: %v", err)
	}
	rt.Handle("GET /site/", http.StripPrefix("/site/", http.FileServer(http.FS(siteFS))))

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
