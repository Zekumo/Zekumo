// Package exports produces player-data exports (GDPR data portability and
// offline analysis): async jobs that write JSON or zipped CSV files to the
// blob storage and hand out presigned download links.
package exports

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"minicloud/internal/httpx"
	"minicloud/internal/repo"
	"minicloud/internal/safego"
	"minicloud/internal/storage"
)

const jobTimeout = 10 * time.Minute

type Service struct {
	DB      *pgxpool.Pool
	Jobs    repo.ExportJobs
	Players repo.Players
	Blob    storage.Storage
}

type playerExport struct {
	repo.Player
	Data         []dataRow    `json:"data"`
	Balances     []balanceRow `json:"balances"`
	Ledger       []ledgerRow  `json:"ledger"`
	Achievements []achRow     `json:"achievements"`
}

type dataRow struct {
	PlayerID  string          `json:"-"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type balanceRow struct {
	PlayerID string `json:"-"`
	Currency string `json:"currency"`
	Balance  int64  `json:"balance"`
}

type ledgerRow struct {
	PlayerID       string    `json:"-"`
	Currency       string    `json:"currency"`
	Amount         int64     `json:"amount"`
	BalanceAfter   int64     `json:"balance_after"`
	Kind           string    `json:"kind"`
	IdempotencyKey string    `json:"idempotency_key"`
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

type achRow struct {
	PlayerID   string     `json:"-"`
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	Progress   int        `json:"progress"`
	UnlockedAt *time.Time `json:"unlocked_at"`
}

func (s *Service) run(jobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()

	job, err := s.Jobs.ByID(ctx, jobID)
	if err != nil {
		return
	}
	_ = s.Jobs.SetRunning(ctx, jobID)

	body, ext, err := s.build(ctx, job)
	if err != nil {
		_ = s.Jobs.SetFailed(ctx, jobID, err.Error())
		return
	}
	key := fmt.Sprintf("exports/%s/%s.%s", job.GameID, job.ID, ext)
	if err := s.Blob.Put(ctx, key, bytes.NewReader(body), int64(len(body))); err != nil {
		_ = s.Jobs.SetFailed(ctx, jobID, "storage write failed: "+err.Error())
		return
	}
	_ = s.Jobs.SetDone(ctx, jobID, key, int64(len(body)))
}

func (s *Service) build(ctx context.Context, job *repo.ExportJob) ([]byte, string, error) {
	players, err := s.players(ctx, job)
	if err != nil {
		return nil, "", err
	}
	byID := make(map[string]*playerExport, len(players))
	out := make([]*playerExport, 0, len(players))
	for i := range players {
		pe := &playerExport{Player: players[i],
			Data: []dataRow{}, Balances: []balanceRow{}, Ledger: []ledgerRow{}, Achievements: []achRow{}}
		byID[pe.ID] = pe
		out = append(out, pe)
	}

	data, err := s.data(ctx, job)
	if err != nil {
		return nil, "", err
	}
	for _, row := range data {
		if pe := byID[row.PlayerID]; pe != nil {
			pe.Data = append(pe.Data, row)
		}
	}
	balances, err := s.balances(ctx, job)
	if err != nil {
		return nil, "", err
	}
	for _, row := range balances {
		if pe := byID[row.PlayerID]; pe != nil {
			pe.Balances = append(pe.Balances, row)
		}
	}
	ledger, err := s.ledger(ctx, job)
	if err != nil {
		return nil, "", err
	}
	for _, row := range ledger {
		if pe := byID[row.PlayerID]; pe != nil {
			pe.Ledger = append(pe.Ledger, row)
		}
	}
	achievements, err := s.achievements(ctx, job)
	if err != nil {
		return nil, "", err
	}
	for _, row := range achievements {
		if pe := byID[row.PlayerID]; pe != nil {
			pe.Achievements = append(pe.Achievements, row)
		}
	}

	if job.Format == "json" {
		body, err := json.MarshalIndent(map[string]any{
			"game_id":      job.GameID,
			"scope":        job.Scope,
			"generated_at": time.Now().UTC(),
			"players":      out,
		}, "", "  ")
		return body, "json", err
	}
	body, err := buildZip(out)
	return body, "zip", err
}

func buildZip(players []*playerExport) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, header []string, rows [][]string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		cw := csv.NewWriter(f)
		if err := cw.Write(header); err != nil {
			return err
		}
		if err := cw.WriteAll(rows); err != nil {
			return err
		}
		cw.Flush()
		return cw.Error()
	}

	pRows, dRows, bRows, lRows, aRows := [][]string{}, [][]string{}, [][]string{}, [][]string{}, [][]string{}
	for _, p := range players {
		pRows = append(pRows, []string{p.ID, p.Provider, p.Identifier, p.Nickname,
			string(p.Profile), strconv.FormatBool(p.Banned),
			p.CreatedAt.Format(time.RFC3339), p.LastLoginAt.Format(time.RFC3339)})
		for _, d := range p.Data {
			dRows = append(dRows, []string{p.ID, d.Key, string(d.Value), d.UpdatedAt.Format(time.RFC3339)})
		}
		for _, b := range p.Balances {
			bRows = append(bRows, []string{p.ID, b.Currency, strconv.FormatInt(b.Balance, 10)})
		}
		for _, l := range p.Ledger {
			lRows = append(lRows, []string{p.ID, l.Currency, strconv.FormatInt(l.Amount, 10),
				strconv.FormatInt(l.BalanceAfter, 10), l.Kind, l.IdempotencyKey, l.Note,
				l.CreatedAt.Format(time.RFC3339)})
		}
		for _, a := range p.Achievements {
			unlocked := ""
			if a.UnlockedAt != nil {
				unlocked = a.UnlockedAt.Format(time.RFC3339)
			}
			aRows = append(aRows, []string{p.ID, a.Key, a.Name, strconv.Itoa(a.Progress), unlocked})
		}
	}
	steps := []struct {
		name   string
		header []string
		rows   [][]string
	}{
		{"players.csv", []string{"id", "provider", "identifier", "nickname", "profile", "banned", "created_at", "last_login_at"}, pRows},
		{"player_data.csv", []string{"player_id", "key", "value", "updated_at"}, dRows},
		{"balances.csv", []string{"player_id", "currency", "balance"}, bRows},
		{"currency_ledger.csv", []string{"player_id", "currency", "amount", "balance_after", "kind", "idempotency_key", "note", "created_at"}, lRows},
		{"achievements.csv", []string{"player_id", "key", "name", "progress", "unlocked_at"}, aRows},
	}
	for _, s := range steps {
		if err := write(s.name, s.header, s.rows); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Service) players(ctx context.Context, job *repo.ExportJob) ([]repo.Player, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, game_id, provider, identifier, nickname, profile, banned, created_at, last_login_at
		 FROM players WHERE game_id=$1 AND ($2::uuid IS NULL OR id=$2) ORDER BY created_at`,
		job.GameID, job.PlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []repo.Player{}
	for rows.Next() {
		var p repo.Player
		if err := rows.Scan(&p.ID, &p.GameID, &p.Provider, &p.Identifier, &p.Nickname,
			&p.Profile, &p.Banned, &p.CreatedAt, &p.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) data(ctx context.Context, job *repo.ExportJob) ([]dataRow, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT d.player_id, d.key, d.value, d.updated_at
		 FROM player_data d JOIN players p ON p.id = d.player_id
		 WHERE p.game_id=$1 AND ($2::uuid IS NULL OR d.player_id=$2)`,
		job.GameID, job.PlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dataRow{}
	for rows.Next() {
		var d dataRow
		if err := rows.Scan(&d.PlayerID, &d.Key, &d.Value, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) balances(ctx context.Context, job *repo.ExportJob) ([]balanceRow, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT b.player_id, c.name, b.balance
		 FROM currency_balances b JOIN currencies c ON c.id = b.currency_id
		 WHERE c.game_id=$1 AND ($2::uuid IS NULL OR b.player_id=$2)`,
		job.GameID, job.PlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []balanceRow{}
	for rows.Next() {
		var b balanceRow
		if err := rows.Scan(&b.PlayerID, &b.Currency, &b.Balance); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Service) ledger(ctx context.Context, job *repo.ExportJob) ([]ledgerRow, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT l.player_id, c.name, l.amount, l.balance_after, l.kind, l.idempotency_key, l.note, l.created_at
		 FROM currency_ledger l JOIN currencies c ON c.id = l.currency_id
		 WHERE c.game_id=$1 AND ($2::uuid IS NULL OR l.player_id=$2) ORDER BY l.id`,
		job.GameID, job.PlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledgerRow{}
	for rows.Next() {
		var l ledgerRow
		if err := rows.Scan(&l.PlayerID, &l.Currency, &l.Amount, &l.BalanceAfter,
			&l.Kind, &l.IdempotencyKey, &l.Note, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Service) achievements(ctx context.Context, job *repo.ExportJob) ([]achRow, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT u.player_id, d.key, d.name, u.progress, u.unlocked_at
		 FROM achievement_unlocks u JOIN achievement_defs d ON d.id = u.achievement_id
		 WHERE d.game_id=$1 AND ($2::uuid IS NULL OR u.player_id=$2)`,
		job.GameID, job.PlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []achRow{}
	for rows.Next() {
		var a achRow
		if err := rows.Scan(&a.PlayerID, &a.Key, &a.Name, &a.Progress, &a.UnlockedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type Handler struct {
	Svc *Service
}

func (h *Handler) filename(job *repo.ExportJob) string {
	ext := "json"
	if job.Format == "csv" {
		ext = "zip"
	}
	return fmt.Sprintf("minicloud-export-%s.%s", job.ID[:8], ext)
}

func (h *Handler) withURL(ctx context.Context, job *repo.ExportJob) map[string]any {
	out := map[string]any{"job": job}
	if job.Status == "done" && job.StorageKey != "" {
		if url, err := h.Svc.Blob.PresignDownload(ctx, job.StorageKey, h.filename(job)); err == nil {
			out["download_url"] = url
		}
	}
	return out
}

// Submit handles POST /admin/api/games/{id}/exports
// Body: {"format":"json"|"csv","scope":"all"|"player","player_id"?}
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	var req struct {
		Format   string `json:"format"`
		Scope    string `json:"scope"`
		PlayerID string `json:"player_id"`
	}
	if httpx.Decode(w, r, &req) != nil {
		return
	}
	if req.Format != "json" && req.Format != "csv" {
		httpx.Error(w, http.StatusBadRequest, "bad_format", `format must be "json" or "csv"`)
		return
	}
	var playerID *string
	switch req.Scope {
	case "all":
	case "player":
		if req.PlayerID == "" {
			httpx.Error(w, http.StatusBadRequest, "missing_field", "player_id is required for player scope")
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
		playerID = &req.PlayerID
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_scope", `scope must be "all" or "player"`)
		return
	}

	job, err := h.Svc.Jobs.Create(r.Context(), gameID, req.Scope, playerID, req.Format)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	safego.Go("exports.job", func() { h.Svc.run(job.ID) })
	httpx.JSON(w, http.StatusAccepted, map[string]any{"job": job})
}

// Status handles GET /admin/api/exports/{job_id}
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	job, err := h.Svc.Jobs.ByID(r.Context(), r.PathValue("job_id"))
	if errors.Is(err, repo.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "export job not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, h.withURL(r.Context(), job))
}

// History handles GET /admin/api/games/{id}/exports?limit=&offset=
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	jobs, err := h.Svc.Jobs.ByGame(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, len(jobs))
	for i := range jobs {
		out[i] = h.withURL(r.Context(), &jobs[i])
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exports": out})
}
