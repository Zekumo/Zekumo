package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

type ctxKey struct{}

// ClaimsFrom returns the verified claims placed in the context by Middleware.
func ClaimsFrom(ctx context.Context) *Claims {
	c, _ := ctx.Value(ctxKey{}).(*Claims)
	return c
}

type Handler struct {
	Issuer   *TokenIssuer
	Registry *Registry
	Games    repo.Games
	Players  repo.Players
	Password PasswordProvider
	Events   Emitter          // optional webhook bus
	Stats    ActivityRecorder // optional stats collector
	Attempts AttemptLimiter   // optional per-identity brute-force guard
	Bans     BanChecker       // optional ban enforcement
}

// BanChecker enforces player bans: a fast Redis blacklist for issued tokens
// and the authoritative database lookup used at login.
type BanChecker interface {
	TokenBanned(ctx context.Context, playerID string) bool
	ActiveBan(ctx context.Context, playerID string) *repo.PlayerBan
}

// AttemptLimiter throttles repeated failures against one identity. Per-IP
// limits alone do not stop a distributed attack on a single account.
type AttemptLimiter interface {
	// Fail records a failure and reports whether the identity is now locked.
	Fail(ctx context.Context, identity string) bool
	// Locked reports whether the identity is currently locked out.
	Locked(ctx context.Context, identity string) bool
	// Succeed clears the failure count after a successful login.
	Succeed(ctx context.Context, identity string)
}

// Emitter publishes platform events; nil disables emission.
type Emitter interface {
	Emit(gameID, event string, data any)
}

// ActivityRecorder feeds the stats pipeline; nil disables collection.
type ActivityRecorder interface {
	RecordActive(gameID, playerID string)
	RecordLogin(gameID string)
	RecordNewPlayer(gameID string)
}

type loginRequest struct {
	AppID string `json:"app_id"`
	Credentials
}

type loginResponse struct {
	Token  string       `json:"token"`
	Player *repo.Player `json:"player"`
}

// Login handles POST /v1/auth/login for every registered provider.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	game, err := h.gameFor(w, r, req.AppID)
	if game == nil {
		return
	}
	provider, err := h.Registry.Get(req.Provider)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "unknown_provider", err.Error())
		return
	}
	// Only credential-based providers can be brute-forced; a guest device id
	// is not a secret, so counting failures against it would be noise.
	identity := ""
	if h.Attempts != nil && req.Username != "" {
		identity = game.ID + ":" + req.Provider + ":" + strings.ToLower(strings.TrimSpace(req.Username))
		if h.Attempts.Locked(r.Context(), identity) {
			httpx.Error(w, http.StatusTooManyRequests, "locked",
				"too many failed attempts for this account, try again later")
			return
		}
	}
	player, err := provider.Authenticate(r.Context(), game.ID, req.Credentials)
	if errors.Is(err, ErrBadCredentials) {
		if identity != "" && h.Attempts.Fail(r.Context(), identity) {
			httpx.Error(w, http.StatusTooManyRequests, "locked",
				"too many failed attempts for this account, try again later")
			return
		}
		httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "wrong username or password")
		return
	}
	if identity != "" {
		h.Attempts.Succeed(r.Context(), identity)
	}
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "auth_failed", err.Error())
		return
	}
	h.finishLogin(w, r, player)
}

// Register handles POST /v1/auth/register for the password provider.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	game, _ := h.gameFor(w, r, req.AppID)
	if game == nil {
		return
	}
	player, err := h.Password.Register(r.Context(), game.ID, req.Credentials)
	if errors.Is(err, ErrUserExists) {
		httpx.Error(w, http.StatusConflict, "user_exists", "username already taken")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "register_failed", err.Error())
		return
	}
	h.finishLogin(w, r, player)
}

func (h *Handler) gameFor(w http.ResponseWriter, r *http.Request, appID string) (*repo.Game, error) {
	if appID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_app_id", "app_id is required")
		return nil, nil
	}
	game, err := h.Games.ByAppID(r.Context(), appID)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "unknown_app", "no game with this app_id")
		return nil, nil
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return nil, nil
	}
	if game.Status != "active" {
		httpx.Error(w, http.StatusForbidden, "game_disabled", "this game is disabled")
		return nil, nil
	}
	return game, nil
}

func (h *Handler) finishLogin(w http.ResponseWriter, r *http.Request, player *repo.Player) {
	if h.Bans != nil {
		if ban := h.Bans.ActiveBan(r.Context(), player.ID); ban != nil {
			httpx.JSON(w, http.StatusForbidden, map[string]any{
				"error": map[string]string{
					"code":    "player_banned",
					"message": "this account is banned: " + ban.Reason,
				},
				"reason":     ban.Reason,
				"expires_at": ban.ExpiresAt,
			})
			return
		}
		if player.Banned {
			_ = h.Players.SetBanned(r.Context(), player.ID, false)
		}
	} else if player.Banned {
		httpx.Error(w, http.StatusForbidden, "banned", "this account is banned")
		return
	}
	token, err := h.Issuer.IssuePlayer(player.ID, player.GameID, player.Nickname)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// created_at == last_login_at only on the row's very first login.
	isNew := player.CreatedAt.Equal(player.LastLoginAt)
	if h.Events != nil {
		info := map[string]any{"player_id": player.ID, "provider": player.Provider, "nickname": player.Nickname}
		if isNew {
			h.Events.Emit(player.GameID, "player.registered", info)
		}
		h.Events.Emit(player.GameID, "player.login", info)
	}
	if h.Stats != nil {
		h.Stats.RecordLogin(player.GameID)
		if isNew {
			h.Stats.RecordNewPlayer(player.GameID)
		}
	}
	_ = h.Players.TouchLogin(r.Context(), player.ID)
	httpx.JSON(w, http.StatusOK, loginResponse{Token: token, Player: player})
}

// Middleware verifies the bearer token (or ?token= for WebSocket upgrades)
// and requires the given role.
func (h *Handler) Middleware(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ""
		if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
			raw = strings.TrimPrefix(header, "Bearer ")
		} else {
			raw = r.URL.Query().Get("token")
		}
		if raw == "" {
			httpx.Error(w, http.StatusUnauthorized, "missing_token", "provide Authorization: Bearer <token>")
			return
		}
		claims, err := h.Issuer.Parse(raw)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
			return
		}
		if claims.Role != role {
			httpx.Error(w, http.StatusForbidden, "wrong_role", "this token cannot access this API")
			return
		}
		if claims.Role == RolePlayer && h.Bans != nil && h.Bans.TokenBanned(r.Context(), claims.Subject) {
			httpx.Error(w, http.StatusForbidden, "player_banned", "this account is banned")
			return
		}
		if claims.Role == RolePlayer && h.Stats != nil {
			h.Stats.RecordActive(claims.GameID, claims.Subject)
		}
		httpx.SetCtxGameID(r.Context(), claims.GameID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
	})
}
