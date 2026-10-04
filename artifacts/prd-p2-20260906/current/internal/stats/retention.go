package stats

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"minicloud/internal/httpx"
)

// Retention snapshots are cohort-based: cohort sizes are refreshed for the
// last 33 days, and each dN column is filled once, when the cohort is old
// enough for the answer to be final. A dN value counts players of that cohort
// whose last_login_at moved past their registration day — "came back at least
// once within N days", measured at the N-day horizon.
type RetentionRow struct {
	Date   string `json:"date"`
	Cohort int    `json:"cohort"`
	D1     *int   `json:"d1"`
	D7     *int   `json:"d7"`
	D30    *int   `json:"d30"`
}

func (s *Service) ComputeRetention(ctx context.Context) {
	tz := s.Loc.String()
	_, err := s.DB.Exec(ctx,
		`INSERT INTO retention_daily (game_id, date, cohort)
		 SELECT game_id, (created_at AT TIME ZONE $1)::date, count(*)
		 FROM players
		 WHERE (created_at AT TIME ZONE $1)::date >= ((now() AT TIME ZONE $1)::date - 33)
		 GROUP BY game_id, (created_at AT TIME ZONE $1)::date
		 ON CONFLICT (game_id, date) DO UPDATE SET cohort = EXCLUDED.cohort`, tz)
	if err != nil {
		log.Printf("stats: retention cohort upsert failed: %v", err)
		return
	}
	for col, minAge := range map[string]int{"d1": 2, "d7": 8, "d30": 31} {
		_, err := s.DB.Exec(ctx,
			`UPDATE retention_daily rd SET `+col+` = (
			   SELECT count(*) FROM players p
			   WHERE p.game_id = rd.game_id
			     AND (p.created_at AT TIME ZONE $1)::date = rd.date
			     AND (p.last_login_at AT TIME ZONE $1)::date > rd.date
			 )
			 WHERE rd.`+col+` IS NULL AND rd.date <= ((now() AT TIME ZONE $1)::date - $2::int)`,
			tz, minAge)
		if err != nil {
			log.Printf("stats: retention %s fill failed: %v", col, err)
		}
	}
}

// StartRetention computes once at boot and then hourly; every statement is
// idempotent, so overlapping instances are harmless.
func (s *Service) StartRetention(ctx context.Context) {
	go func() {
		s.ComputeRetention(ctx)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.ComputeRetention(ctx)
			}
		}
	}()
}

func (s *Service) RetentionRows(ctx context.Context, gameID string, days int) ([]RetentionRow, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT to_char(date, 'YYYY-MM-DD'), cohort, d1, d7, d30
		 FROM retention_daily
		 WHERE game_id = $1 AND date >= ((now() AT TIME ZONE $2)::date - ($3::int - 1))
		 ORDER BY date`,
		gameID, s.Loc.String(), days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RetentionRow{}
	for rows.Next() {
		var r RetentionRow
		if err := rows.Scan(&r.Date, &r.Cohort, &r.D1, &r.D7, &r.D30); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type FunnelStep struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Funnel aggregates cohorts old enough to have a final 7-day answer.
// Registration and first login coincide on this platform (a player row is
// created by the first successful login), so the second step equals the first.
func (s *Service) Funnel(ctx context.Context, gameID string, days int) ([]FunnelStep, error) {
	var registered, returned7 int
	err := s.DB.QueryRow(ctx,
		`SELECT COALESCE(sum(cohort), 0), COALESCE(sum(d7), 0)
		 FROM retention_daily
		 WHERE game_id = $1 AND d7 IS NOT NULL
		   AND date >= ((now() AT TIME ZONE $2)::date - ($3::int - 1))`,
		gameID, s.Loc.String(), days).Scan(&registered, &returned7)
	if err != nil {
		return nil, err
	}
	return []FunnelStep{
		{Name: "registered", Count: registered},
		{Name: "first_login", Count: registered},
		{Name: "returned_7d", Count: returned7},
	}, nil
}

// Retention handles GET /admin/api/games/{id}/stats/retention?days=30.
func (h *Handler) Retention(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	rows, err := h.Svc.RetentionRows(r.Context(), r.PathValue("id"), days)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"days":     rows,
		"timezone": h.Svc.Loc.String(),
	})
}

// Funnel handles GET /admin/api/games/{id}/stats/funnel?days=30.
func (h *Handler) Funnel(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	steps, err := h.Svc.Funnel(r.Context(), r.PathValue("id"), days)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"steps":    steps,
		"window":   days,
		"timezone": h.Svc.Loc.String(),
	})
}
