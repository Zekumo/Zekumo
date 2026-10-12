package stats

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"zekumo/internal/httpx"
)

// Retention reports exact calendar-day activity (D1/D7/D30), not cumulative
// last-login approximations. Cohorts before complete tracking remain unknown.
type RetentionRow struct {
	Measurement string `json:"measurement"`
	Date        string `json:"date"`
	Cohort      int    `json:"cohort"`
	D1          *int   `json:"d1"`
	D7          *int   `json:"d7"`
	D30         *int   `json:"d30"`
}

func (s *Service) ComputeRetention(ctx context.Context) {
	tz := s.Loc.String()
	if _, err := s.DB.Exec(ctx, `INSERT INTO stats_activity_tracking(timezone) VALUES($1) ON CONFLICT DO NOTHING`, tz); err != nil {
		log.Printf("stats: tracking initialization failed: %v", err)
		return
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO retention_daily(game_id,date,cohort,measurement,stats_timezone)
 SELECT p.game_id,(p.created_at AT TIME ZONE $1)::date,count(*),
 CASE WHEN (p.created_at AT TIME ZONE $1)::date>(SELECT (started_at AT TIME ZONE $1)::date FROM stats_activity_tracking WHERE timezone=$1)
 THEN 'exact_day' ELSE 'insufficient_history' END,$1
 FROM players p WHERE (p.created_at AT TIME ZONE $1)::date>=((now() AT TIME ZONE $1)::date-93)
 GROUP BY p.game_id,(p.created_at AT TIME ZONE $1)::date
 ON CONFLICT(game_id,date) DO UPDATE SET cohort=EXCLUDED.cohort,measurement=EXCLUDED.measurement,stats_timezone=EXCLUDED.stats_timezone`, tz)
	if err != nil {
		log.Printf("stats: retention cohort upsert failed: %v", err)
		return
	}
	for col, day := range map[string]int{"d1": 1, "d7": 7, "d30": 30} {
		_, err = s.DB.Exec(ctx, `UPDATE retention_daily rd SET `+col+`=(
   SELECT count(*) FROM players p WHERE p.game_id=rd.game_id
   AND (p.created_at AT TIME ZONE $1)::date=rd.date
   AND EXISTS(SELECT 1 FROM player_activity_daily a WHERE a.game_id=p.game_id AND a.player_id=p.id
    AND a.timezone=$1 AND a.activity_date=rd.date+$2::int))
   WHERE rd.measurement='exact_day' AND rd.stats_timezone=$1
   AND rd.date<=((now() AT TIME ZONE $1)::date-($2::int+1))
   AND rd.date>=((now() AT TIME ZONE $1)::date-93)`, tz, day)
		if err != nil {
			log.Printf("stats: retention %s computation failed: %v", col, err)
		}
	}
	_, err = s.DB.Exec(ctx, `UPDATE retention_daily rd SET returned7=(
  SELECT count(*) FROM players p WHERE p.game_id=rd.game_id AND (p.created_at AT TIME ZONE $1)::date=rd.date
  AND EXISTS(SELECT 1 FROM player_activity_daily a WHERE a.game_id=p.game_id AND a.player_id=p.id
   AND a.timezone=$1 AND a.activity_date BETWEEN rd.date+1 AND rd.date+7))
  WHERE rd.measurement='exact_day' AND rd.stats_timezone=$1
  AND rd.date<=((now() AT TIME ZONE $1)::date-8) AND rd.date>=((now() AT TIME ZONE $1)::date-93)`, tz)
	if err != nil {
		log.Printf("stats: seven-day return computation failed: %v", err)
	}
	// Retain facts long enough for the 90-day console range plus finalization.
	if _, err = s.DB.Exec(ctx, `DELETE FROM player_activity_daily WHERE activity_date<((now() AT TIME ZONE $1)::date-100)`, tz); err != nil {
		log.Printf("stats: activity cleanup failed: %v", err)
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
		`SELECT to_char(date, 'YYYY-MM-DD'), cohort,
 CASE WHEN measurement='exact_day' AND stats_timezone=$2 THEN d1 END,
 CASE WHEN measurement='exact_day' AND stats_timezone=$2 THEN d7 END,
 CASE WHEN measurement='exact_day' AND stats_timezone=$2 THEN d30 END,
 CASE WHEN stats_timezone=$2 THEN measurement ELSE 'insufficient_history' END
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
		if err := rows.Scan(&r.Date, &r.Cohort, &r.D1, &r.D7, &r.D30, &r.Measurement); err != nil {
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
		`SELECT COALESCE(sum(cohort), 0), COALESCE(sum(returned7), 0)
		 FROM retention_daily
		 WHERE game_id = $1 AND returned7 IS NOT NULL AND measurement='exact_day' AND stats_timezone=$2
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
		"days":            rows,
		"timezone":        h.Svc.Loc.String(),
		"measurement":     "exact_calendar_day",
		"historical_note": "Cohorts before complete daily tracking have unknown retention; no historical activity was fabricated.",
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
		"steps":       steps,
		"window":      days,
		"timezone":    h.Svc.Loc.String(),
		"measurement": "returned_within_days_1_to_7",
	})
}
