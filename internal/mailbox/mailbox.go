// Package mailbox implements operator-to-player in-game mail with optional
// currency rewards claimed atomically through the wallet ledger.
package mailbox

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"zekumo/internal/auth"
	"zekumo/internal/httpx"
	"zekumo/internal/repo"
)

const (
	maxRecipients    = 1000
	maxInbox         = 100
	defaultExpiresIn = 30 * 24 * time.Hour
	maxExpiresIn     = 365 * 24 * time.Hour
)

type Handler struct {
	DB         *pgxpool.Pool
	Mail       repo.Mail
	Wallets    repo.Wallets
	Currencies repo.Currencies
}

func (h *Handler) mailForPlayer(w http.ResponseWriter, r *http.Request) *repo.MailMessage {
	claims := auth.ClaimsFrom(r.Context())
	m, err := h.Mail.ByID(r.Context(), r.PathValue("mid"))
	if errors.Is(err, repo.ErrNotFound) || (err == nil && m.GameID != claims.GameID) {
		httpx.Error(w, http.StatusNotFound, "not_found", "mail not found")
		return nil
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return nil
	}
	if time.Now().After(m.ExpiresAt) {
		httpx.Error(w, http.StatusGone, "expired", "this mail has expired")
		return nil
	}
	return m
}

// Inbox handles GET /v1/mailbox?unread_only=true
func (h *Handler) Inbox(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	unreadOnly := r.URL.Query().Get("unread_only") == "true"
	items, err := h.Mail.Inbox(r.Context(), claims.GameID, claims.Subject, unreadOnly, maxInbox)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	unread, err := h.Mail.UnreadCount(r.Context(), claims.GameID, claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"mail": items, "unread": unread})
}

// Read handles POST /v1/mailbox/{mid}/read
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	m := h.mailForPlayer(w, r)
	if m == nil {
		return
	}
	if err := h.Mail.MarkRead(r.Context(), m, claims.Subject); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "mail not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "read"})
}

// Claim handles POST /v1/mailbox/{mid}/claim — one-shot reward collection.
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	m := h.mailForPlayer(w, r)
	if m == nil {
		return
	}
	var rewards []repo.MailReward
	if err := json.Unmarshal(m.Rewards, &rewards); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "corrupt rewards payload")
		return
	}

	ctx := r.Context()
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	claimedAt, err := h.Mail.ClaimTx(ctx, tx, m, claims.Subject)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrAlreadyClaimed):
			httpx.Error(w, http.StatusConflict, "already_claimed", "rewards were already claimed")
		case errors.Is(err, repo.ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "not_found", "mail not found")
		default:
			httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}

	granted := make([]map[string]any, 0, len(rewards))
	for _, rw := range rewards {
		balance, err := h.Wallets.ApplyTx(ctx, tx, rw.CurrencyID, claims.Subject, rw.Amount,
			"mail", "mail:"+m.ID, "mail reward: "+m.Title)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		granted = append(granted, map[string]any{
			"currency_id": rw.CurrencyID, "amount": rw.Amount, "balance": balance,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"claimed_at": claimedAt, "rewards": granted})
}

// AdminSend handles POST /admin/api/games/{id}/mail
// Body: {"title","body","recipients":"all"|[ids],"rewards"?,"expires_in"?}
func (h *Handler) AdminSend(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	var req struct {
		Title      string            `json:"title"`
		Body       string            `json:"body"`
		Recipients json.RawMessage   `json:"recipients"`
		Rewards    []repo.MailReward `json:"rewards"`
		ExpiresIn  int64             `json:"expires_in"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Title == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "title is required")
		return
	}

	broadcast := false
	var recipients []string
	var all string
	if json.Unmarshal(req.Recipients, &all) == nil && all == "all" {
		broadcast = true
	} else if err := json.Unmarshal(req.Recipients, &recipients); err != nil || len(recipients) == 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_recipients", `recipients must be "all" or a non-empty array of player ids`)
		return
	}
	if len(recipients) > maxRecipients {
		httpx.Error(w, http.StatusBadRequest, "too_many_recipients", "at most 1000 recipients per mail")
		return
	}

	if len(req.Rewards) > 8 {
		httpx.Error(w, http.StatusBadRequest, "bad_reward", "at most 8 rewards per mail")
		return
	}
	seen := map[string]bool{}
	for _, rw := range req.Rewards {
		if rw.Amount <= 0 {
			httpx.Error(w, http.StatusBadRequest, "bad_reward", "reward amounts must be positive")
			return
		}
		if seen[rw.CurrencyID] {
			httpx.Error(w, http.StatusBadRequest, "bad_reward", "duplicate currency in rewards: "+rw.CurrencyID)
			return
		}
		seen[rw.CurrencyID] = true
		cur, err := h.Currencies.ByID(r.Context(), rw.CurrencyID)
		if errors.Is(err, repo.ErrNotFound) || (err == nil && cur.GameID != gameID) {
			httpx.Error(w, http.StatusBadRequest, "bad_reward", "unknown currency: "+rw.CurrencyID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	rewards, _ := json.Marshal(req.Rewards)
	if req.Rewards == nil {
		rewards = []byte("[]")
	}

	expiresIn := time.Duration(req.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = defaultExpiresIn
	}
	if expiresIn > maxExpiresIn {
		expiresIn = maxExpiresIn
	}

	m, err := h.Mail.Create(r.Context(), gameID, broadcast, req.Title, req.Body, rewards, time.Now().Add(expiresIn))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	delivered := 0
	if !broadcast {
		if delivered, err = h.Mail.AddRecipients(r.Context(), m.ID, gameID, recipients); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"mail": m, "delivered": delivered})
}

// AdminList handles GET /admin/api/games/{id}/mail?limit=&offset=
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, err := h.Mail.AdminList(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"mail": items})
}
