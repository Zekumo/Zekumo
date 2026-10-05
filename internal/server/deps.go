package server

import (
	"context"
	"log"
	"time"

	"zekumo/internal/achievements"
	"zekumo/internal/admin"
	"zekumo/internal/announcements"
	"zekumo/internal/auth"
	"zekumo/internal/bans"
	"zekumo/internal/chat"
	"zekumo/internal/config"
	"zekumo/internal/currency"
	"zekumo/internal/dialogue"
	"zekumo/internal/exports"
	"zekumo/internal/friends"
	"zekumo/internal/funcs"
	"zekumo/internal/hooks"
	"zekumo/internal/kv"
	"zekumo/internal/leaderboard"
	"zekumo/internal/logs"
	"zekumo/internal/mailbox"
	"zekumo/internal/oauth"
	"zekumo/internal/player"
	"zekumo/internal/ratelimit"
	"zekumo/internal/realtime"
	"zekumo/internal/repo"
	"zekumo/internal/sso"
	"zekumo/internal/stats"
	"zekumo/internal/storage"
	"zekumo/internal/store"
	"zekumo/internal/tenant"
	"zekumo/internal/updates"
)

// deps holds everything the route table needs. Building it is kept separate
// from wiring routes so each stays readable on its own.
type deps struct {
	blob  storage.Storage
	store *store.Store // readiness probes ping the dependencies directly

	limiter ratelimit.Limiter
	logs    *logs.Service
	hub     *realtime.Hub

	authH          *auth.Handler
	ssoH           *sso.Handler
	oauthH         *oauth.Handler
	playerH        *player.Handler
	lbH            *leaderboard.Handler
	dialogueH      *dialogue.Handler
	chatH          *chat.Handler
	fnH            *funcs.Handler
	updatesH       *updates.Handler
	adminH         *admin.Handler
	hooksH         *hooks.Handler
	logsH          *logs.Handler
	statsH         *stats.Handler
	healthH        *stats.HealthHandler
	friendsH       *friends.Handler
	achievementsH  *achievements.Handler
	announcementsH *announcements.Handler
	currencyH      *currency.Handler
	mailH          *mailbox.Handler
	bansH          *bans.Handler
	kvH            *kv.Handler
	exportsH       *exports.Handler
	tenantH        *tenant.Handler
	tenants        tenant.Store
}

// newDeps constructs the services and starts the background workers. Every
// worker is bound to ctx, so they stop when the server does.
func newDeps(ctx context.Context, cfg config.Config, st *store.Store, blob storage.Storage) *deps {
	tenants := tenant.Store{DB: st.DB}
	games := repo.Games{DB: st.DB}
	players := repo.Players{DB: st.DB}
	data := repo.PlayerData{DB: st.DB}
	accounts := repo.Accounts{DB: st.DB}

	logSvc := logs.NewService(ctx, st.DB, cfg.LogRetentionDays)
	logSvc.StartRetention(ctx)

	bus := hooks.NewBus(repo.Webhooks{DB: st.DB}, repo.WebhookDeliveries{DB: st.DB}, cfg.FuncHTTPAllowPrivate)
	bus.Logs = logSvc

	statsLoc, err := time.LoadLocation(cfg.StatsTZ)
	if err != nil {
		log.Printf("stats: unknown timezone %q, falling back to UTC", cfg.StatsTZ)
		statsLoc = time.UTC
	}
	statsSvc := &stats.Service{RDB: st.RDB, DB: st.DB, Games: games, Loc: statsLoc}
	statsSvc.StartAggregator(ctx)
	statsSvc.StartRetention(ctx)

	currencySvc := &currency.Service{
		Currencies: repo.Currencies{DB: st.DB},
		Wallets:    repo.Wallets{DB: st.DB},
		Players:    players,
	}
	bansSvc := &bans.Service{Bans: repo.Bans{DB: st.DB}, Players: players, RDB: st.RDB}

	kvH := &kv.Handler{Games: games, Data: repo.GameData{DB: st.DB}}
	kvH.StartSweeper(ctx)

	exportsSvc := &exports.Service{
		DB: st.DB, Jobs: repo.ExportJobs{DB: st.DB}, Players: players, Blob: blob,
	}
	if err := exportsSvc.Jobs.FailStale(ctx); err != nil {
		log.Printf("exports: fail stale jobs: %v", err)
	}

	limiter := ratelimit.Limiter{RDB: st.RDB, TrustProxy: cfg.TrustProxy}
	attempts := ratelimit.NewAttempts(st.RDB)

	issuer := auth.NewTokenIssuer(cfg.JWTSecret, cfg.TokenTTL)
	tickets := sso.Tickets{RDB: st.RDB}
	passwordProvider := auth.PasswordProvider{Players: players}
	ssoProvider := sso.Provider{Accounts: accounts, Players: players, Tickets: tickets}

	chatSvc := chat.NewService(repo.Chat{DB: st.DB}, cfg.BannedWordsFile)
	hub := realtime.NewHub(chatSvc, issuer)

	lbSvc := &leaderboard.Service{RDB: st.RDB, Scores: repo.Scores{DB: st.DB}, Players: players}
	fnRuntime := &funcs.Runtime{
		GameData: repo.GameData{DB: st.DB}, PlayerData: data, Players: players, Games: games,
		Leaderboard: lbSvc, Currency: currencySvc,
		AllowPrivateHTTP: cfg.FuncHTTPAllowPrivate, Logs: logSvc,
	}
	fnHandler := &funcs.Handler{
		Functions: repo.Functions{DB: st.DB}, Games: games, Players: players,
		Runtime: fnRuntime, Logs: logSvc,
	}
	(&funcs.Scheduler{Functions: fnHandler.Functions, Runtime: fnRuntime, Logs: logSvc}).Start(ctx)

	return &deps{
		blob:    blob,
		store:   st,
		limiter: limiter,
		logs:    logSvc,
		hub:     hub,
		tenants: tenants,
		authH: &auth.Handler{
			Issuer:   issuer,
			Registry: auth.NewRegistry(auth.GuestProvider{Players: players}, passwordProvider, ssoProvider),
			Games:    games, Players: players, Password: passwordProvider,
			Events: bus, Stats: statsSvc, Attempts: attempts, Bans: bansSvc,
		},
		ssoH: &sso.Handler{
			Issuer: issuer, Accounts: accounts, Players: players, Games: games,
			Tickets: tickets, Attempts: attempts,
		},
		oauthH: &oauth.Handler{
			Issuer: issuer, RDB: st.RDB, Accounts: accounts,
			Clients: repo.OAuthClients{DB: st.DB}, Grants: repo.OAuthGrants{DB: st.DB},
			RefreshTokens: repo.OAuthRefreshTokens{DB: st.DB},
		},
		playerH:   &player.Handler{Players: players, Data: data},
		lbH:       &leaderboard.Handler{Svc: lbSvc, Events: bus, Friends: repo.Friendships{DB: st.DB}},
		dialogueH: &dialogue.Handler{Dialogues: repo.Dialogues{DB: st.DB}},
		chatH:     &chat.Handler{Svc: chatSvc},
		fnH:       fnHandler,
		updatesH: &updates.Handler{
			Games: games, Releases: repo.Releases{DB: st.DB}, Artifacts: repo.Artifacts{DB: st.DB},
			Storage: blob, MaxArtifactSize: cfg.MaxArtifactSize, Events: bus,
		},
		adminH: &admin.Handler{
			Issuer: issuer, User: cfg.AdminUser, Pass: cfg.AdminPass,
			Games: games, Players: players, Accounts: accounts, Data: data,
			Dialogues: repo.Dialogues{DB: st.DB}, OAuthClients: repo.OAuthClients{DB: st.DB}, Hub: hub,
			Tenants:          tenants,
			DefaultJWTSecret: cfg.JWTSecret == config.DefaultJWTSecret,
		},
		hooksH:  &hooks.Handler{Bus: bus},
		logsH:   &logs.Handler{Svc: logSvc},
		statsH:  &stats.Handler{Svc: statsSvc, Online: hub.OnlineCount},
		healthH: &stats.HealthHandler{Svc: statsSvc, StartedAt: time.Now(), Version: Version, Env: cfg.Env},
		friendsH: &friends.Handler{
			Svc: &friends.Service{
				Requests:    repo.FriendRequests{DB: st.DB},
				Friendships: repo.Friendships{DB: st.DB},
				Blocks:      repo.PlayerBlocks{DB: st.DB},
				Players:     players,
				Games:       games,
			},
			Hub: hub,
		},
		achievementsH: &achievements.Handler{
			Svc: &achievements.Service{
				Defs:    repo.AchievementDefs{DB: st.DB},
				Unlocks: repo.AchievementUnlocks{DB: st.DB},
			},
			Events: bus,
		},
		announcementsH: &announcements.Handler{Repo: repo.Announcements{DB: st.DB}},
		currencyH:      &currency.Handler{Svc: currencySvc},
		mailH: &mailbox.Handler{
			DB: st.DB, Mail: repo.Mail{DB: st.DB},
			Wallets: repo.Wallets{DB: st.DB}, Currencies: repo.Currencies{DB: st.DB},
		},
		bansH:    &bans.Handler{Svc: bansSvc, Events: bus},
		kvH:      kvH,
		exportsH: &exports.Handler{Svc: exportsSvc},
		tenantH:  &tenant.Handler{Store: tenants, Accounts: accounts},
	}
}
