// Package currency implements per-game virtual currencies: definitions
// managed via the admin API, and an idempotent grant/spend ledger.
package currency

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"minicloud/internal/auth"
	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

const (
	MaxPerGame        = 8
	maxKeyLen         = 128
	maxNameLen        = 32
	maxDisplayNameLen = 100
)

var ErrInvalidTransaction = errors.New("invalid currency transaction")

type Service struct {
	Currencies repo.Currencies
	Wallets    repo.Wallets
	Players    repo.Players
}

// GrantByName credits a player, resolving the currency by its short name.
// Used by the cloud-function runtime.
func (s *Service) GrantByName(ctx context.Context, gameID, name, playerID string, amount int64, idemKey, note string) (int64, bool, error) {
	name = strings.TrimSpace(name)
	playerID = strings.TrimSpace(playerID)
	idemKey = strings.TrimSpace(idemKey)
	if name == "" || playerID == "" || amount <= 0 || idemKey == "" || len(idemKey) > maxKeyLen {
		return 0, false, ErrInvalidTransaction
	}
	cur, err := s.Currencies.ByName(ctx, gameID, name)
	if err != nil {
		return 0, false, err
	}
	p, err := s.Players.ByID(ctx, playerID)
	if err != nil || p.GameID != gameID {
		return 0, false, repo.ErrNotFound
	}
	return s.Wallets.Apply(ctx, cur.ID, playerID, amount, "grant", idemKey, note)
}

type Handler struct {
	Svc *Service
}

func (h *Handler) currencyInGame(w http.ResponseWriter, r *http.Request, cid, gameID string) *repo.Currency {
	cur, err := h.Svc.Currencies.ByID(r.Context(), cid)
	if errors.Is(err, repo.ErrNotFound) || (err == nil && cur.GameID != gameID) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no such currency in this game")
		return nil
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return nil
	}
	return cur
}

type txRequest struct {
	PlayerID       string `json:"player_id"`
	Amount         int64  `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
	Note           string `json:"note"`
}

func (h *Handler) decodeTx(w http.ResponseWriter, r *http.Request) *txRequest {
	var req txRequest
	if httpx.Decode(w, r, &req) != nil {
		return nil
	}
	if req.Amount <= 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_amount", "amount must be positive")
		return nil
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > maxKeyLen {
		httpx.Error(w, http.StatusBadRequest, "bad_idempotency_key", "idempotency_key is required (max 128 chars)")
		return nil
	}
	return &req
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request, cur *repo.Currency, playerID string, amount int64, kind, idemKey, note string) {
	balance, duplicate, err := h.Svc.Wallets.Apply(r.Context(), cur.ID, playerID, amount, kind, idemKey, note)
	switch {
	case errors.Is(err, repo.ErrInsufficientFunds):
		httpx.Error(w, http.StatusPaymentRequired, "insufficient_funds", "balance is too low")
	case errors.Is(err, repo.ErrIdemConflict):
		httpx.Error(w, http.StatusConflict, "idempotency_conflict", "idempotency_key was already used with a different amount")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{
			"currency_id": cur.ID, "balance": balance, "duplicate": duplicate,
		})
	}
}

// MyBalances handles GET /v1/currency
func (h *Handler) MyBalances(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	balances, err := h.Svc.Wallets.Balances(r.Context(), claims.GameID, claims.Subject)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"balances": balances})
}

// MyLedger handles GET /v1/currency/{cid}/ledger?limit=&offset=
func (h *Handler) MyLedger(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	cur := h.currencyInGame(w, r, r.PathValue("cid"), claims.GameID)
	if cur == nil {
		return
	}
	h.writeLedger(w, r, cur.ID, claims.Subject)
}

func (h *Handler) writeLedger(w http.ResponseWriter, r *http.Request, currencyID, playerID string) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	entries, err := h.Svc.Wallets.Ledger(r.Context(), currencyID, playerID, limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// Spend handles POST /v1/currency/{cid}/spend
func (h *Handler) Spend(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFrom(r.Context())
	cur := h.currencyInGame(w, r, r.PathValue("cid"), claims.GameID)
	if cur == nil {
		return
	}
	req := h.decodeTx(w, r)
	if req == nil {
		return
	}
	h.apply(w, r, cur, claims.Subject, -req.Amount, "spend", req.IdempotencyKey, req.Note)
}

// AdminGrant handles POST /admin/api/games/{id}/currency/{cid}/grant
func (h *Handler) AdminGrant(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	cur := h.currencyInGame(w, r, r.PathValue("cid"), gameID)
	if cur == nil {
		return
	}
	req := h.decodeTx(w, r)
	if req == nil {
		return
	}
	if req.PlayerID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_field", "player_id is required")
		return
	}
	p, err := h.Svc.Players.ByID(r.Context(), req.PlayerID)
	if errors.Is(err, repo.ErrNotFound) || (err == nil && p.GameID != gameID) {
		httpx.Error(w, http.StatusNotFound, "player_not_found", "no such player in this game")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	h.apply(w, r, cur, req.PlayerID, req.Amount, "grant", req.IdempotencyKey, req.Note)
}

// AdminList handles GET /admin/api/games/{id}/currencies
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.Currencies.ByGame(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"currencies": list})
}

// AdminCreate handles POST /admin/api/games/{id}/currencies
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	var body repo.Currency
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > maxNameLen {
		httpx.Error(w, http.StatusBadRequest, "bad_name", "name is required (max 32 chars)")
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Name
	}
	if utf8.RuneCountInString(body.DisplayName) > maxDisplayNameLen {
		httpx.Error(w, http.StatusBadRequest, "bad_display_name", "display_name must be at most 100 chars")
		return
	}
	cur, err := h.Svc.Currencies.CreateLimited(r.Context(), gameID, body, MaxPerGame)
	if err != nil {
		if errors.Is(err, repo.ErrCurrencyLimit) {
			httpx.Error(w, http.StatusUnprocessableEntity, "limit_reached", "a game can define at most 8 currencies")
			return
		}
		if repo.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "name_exists", "a currency with this name already exists")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, cur)
}

// AdminUpdate handles PUT /admin/api/currencies/{cid}
func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	var body repo.Currency
	if httpx.Decode(w, r, &body) != nil {
		return
	}
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	if body.DisplayName == "" || utf8.RuneCountInString(body.DisplayName) > maxDisplayNameLen {
		httpx.Error(w, http.StatusBadRequest, "bad_display_name", "display_name is required (max 100 chars)")
		return
	}
	cur, err := h.Svc.Currencies.Update(r.Context(), r.PathValue("cid"), body)
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "currency not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, cur)
}

// AdminDelete handles DELETE /admin/api/currencies/{cid}
func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Currencies.Delete(r.Context(), r.PathValue("cid")); err != nil {
		if errors.Is(err, repo.ErrCurrencyInUse) {
			httpx.Error(w, http.StatusConflict, "currency_in_use", "currency has balances, ledger entries or pending mail rewards")
			return
		}
		if errors.Is(err, repo.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "currency not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminPlayerBalances handles GET /admin/api/players/{pid}/currency
func (h *Handler) AdminPlayerBalances(w http.ResponseWriter, r *http.Request) {
	p, err := h.Svc.Players.ByID(r.Context(), r.PathValue("pid"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "player not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	balances, err := h.Svc.Wallets.Balances(r.Context(), p.GameID, p.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"balances": balances})
}

// AdminPlayerLedger handles GET /admin/api/players/{pid}/currency/{cid}/ledger
func (h *Handler) AdminPlayerLedger(w http.ResponseWriter, r *http.Request) {
	p, err := h.Svc.Players.ByID(r.Context(), r.PathValue("pid"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "player not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cur := h.currencyInGame(w, r, r.PathValue("cid"), p.GameID)
	if cur == nil {
		return
	}
	h.writeLedger(w, r, cur.ID, p.ID)
}
