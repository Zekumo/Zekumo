// Package kv exposes the game-level key-value store over REST. Reads use a
// player token and are limited to public namespaces; writes are server-side
// only, authenticated by an HMAC signature made with the game's app_secret.
package kv

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

const (
	maxValueSize  = 64 << 10
	signatureSkew = 5 * time.Minute
	maxTTLSecs    = 365 * 24 * 3600
	sweepInterval = time.Hour
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

type Handler struct {
	Games repo.Games
	Data  repo.GameData
}

// StartSweeper deletes expired keys periodically; reads already filter them,
// so this only reclaims space.
func (h *Handler) StartSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := h.Data.DeleteExpired(ctx); err != nil {
					log.Printf("kv: expiry sweep failed: %v", err)
				} else if n > 0 {
					log.Printf("kv: swept %d expired keys", n)
				}
			}
		}
	}()
}

func isPublicNamespace(ns string) bool {
	return ns == "public" || strings.HasPrefix(ns, "public_")
}

func pathNames(w http.ResponseWriter, r *http.Request, needKey bool) (ns, key string, ok bool) {
	ns, key = r.PathValue("namespace"), r.PathValue("key")
	if !namePattern.MatchString(ns) || (needKey && !namePattern.MatchString(key)) {
		httpx.Error(w, http.StatusBadRequest, "bad_name",
			"namespace and key must match [a-zA-Z0-9_.-]{1,64}")
		return "", "", false
	}
	return ns, key, true
}

// verifySigned authenticates a server-side request: headers X-Zekumo-App-Id,
// X-Zekumo-Timestamp (unix seconds, ±5 min) and X-Zekumo-Signature =
// "sha256=" + hex(HMAC-SHA256(app_secret, timestamp + "." + method + "." + path + "." + body)).
func (h *Handler) verifySigned(w http.ResponseWriter, r *http.Request, body []byte) *repo.Game {
	appID := r.Header.Get("X-Zekumo-App-Id")
	tsRaw := r.Header.Get("X-Zekumo-Timestamp")
	sig := strings.TrimPrefix(r.Header.Get("X-Zekumo-Signature"), "sha256=")
	if appID == "" || tsRaw == "" || sig == "" {
		httpx.Error(w, http.StatusUnauthorized, "missing_signature",
			"X-Zekumo-App-Id, X-Zekumo-Timestamp and X-Zekumo-Signature headers are required")
		return nil
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > signatureSkew {
		httpx.Error(w, http.StatusUnauthorized, "bad_timestamp", "timestamp is invalid or outside the 5 minute window")
		return nil
	}
	game, err := h.Games.ByAppID(r.Context(), appID)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusUnauthorized, "unknown_app", "no game with this app_id")
		return nil
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return nil
	}
	mac := hmac.New(sha256.New, []byte(game.AppSecret))
	mac.Write([]byte(tsRaw + "." + r.Method + "." + r.URL.Path + "."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(sig))) {
		httpx.Error(w, http.StatusUnauthorized, "bad_signature", "signature verification failed")
		return nil
	}
	if game.Status != "active" {
		httpx.Error(w, http.StatusForbidden, "game_disabled", "this game is disabled")
		return nil
	}
	return game
}

// Get handles GET /v1/kv/{namespace}/{key} (player token, public namespaces).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	ns, key, ok := pathNames(w, r, true)
	if !ok {
		return
	}
	if !isPublicNamespace(ns) {
		httpx.Error(w, http.StatusForbidden, "private_namespace",
			`players may only read the "public" namespace or namespaces prefixed "public_"`)
		return
	}
	value, err := h.Data.Get(r.Context(), claims.GameID, ns+"/"+key)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no value under this key")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"key": key, "namespace": ns, "value": value})
}

// List handles GET /v1/kv/{namespace}?limit=&offset= (player token, public namespaces).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	ns, _, ok := pathNames(w, r, false)
	if !ok {
		return
	}
	if !isPublicNamespace(ns) {
		httpx.Error(w, http.StatusForbidden, "private_namespace",
			`players may only read the "public" namespace or namespaces prefixed "public_"`)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	entries, err := h.Data.ListPrefix(r.Context(), claims.GameID, ns+"/", limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"namespace": ns, "keys": entries})
}

// Put handles PUT /v1/kv/{namespace}/{key}?ttl= (app_secret signature).
// The request body is the JSON value itself.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	ns, key, ok := pathNames(w, r, true)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxValueSize+1))
	if err != nil || len(body) > maxValueSize {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "value_too_large", "value exceeds 64KB")
		return
	}
	if !json.Valid(body) {
		httpx.Error(w, http.StatusBadRequest, "bad_json", "body must be valid JSON")
		return
	}
	game := h.verifySigned(w, r, body)
	if game == nil {
		return
	}
	var expiresAt *time.Time
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		ttl, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ttl <= 0 || ttl > maxTTLSecs {
			httpx.Error(w, http.StatusBadRequest, "bad_ttl", "ttl must be 1..31536000 seconds")
			return
		}
		t := time.Now().Add(time.Duration(ttl) * time.Second)
		expiresAt = &t
	}
	if err := h.Data.PutTTL(r.Context(), game.ID, ns+"/"+key, body, expiresAt); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	res := map[string]any{"key": key, "namespace": ns, "stored": true}
	if expiresAt != nil {
		res["expires_at"] = expiresAt
	}
	httpx.JSON(w, http.StatusOK, res)
}

// Delete handles DELETE /v1/kv/{namespace}/{key} (app_secret signature).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ns, key, ok := pathNames(w, r, true)
	if !ok {
		return
	}
	game := h.verifySigned(w, r, nil)
	if game == nil {
		return
	}
	if err := h.Data.Delete(r.Context(), game.ID, ns+"/"+key); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
