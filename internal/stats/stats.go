// Package stats collects per-game daily activity (DAU, logins, new players).
// Hot counters live in Redis and are periodically flushed into Postgres.
// Every day-boundary computation goes through one location-aware dayKey so
// writes and reads can never disagree on the timezone.
package stats

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"minicloud/internal/httpx"
	"minicloud/internal/repo"
)

const counterTTL = 72 * time.Hour

type Service struct {
	RDB   *redis.Client
	DB    *pgxpool.Pool
	Games repo.Games
	Loc   *time.Location
}

func (s *Service) dayKey(t time.Time) string {
	return t.In(s.Loc).Format("2006-01-02")
}

// dayKeyOffset moves in calendar days rather than 24-hour chunks. This keeps
// reporting windows correct across daylight-saving transitions.
func (s *Service) dayKeyOffset(t time.Time, days int) string {
	local := t.In(s.Loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.Loc)
	return day.AddDate(0, 0, days).Format("2006-01-02")
}

func (s *Service) key(gameID, day, kind string) string {
	return "st:" + gameID + ":" + day + ":" + kind
}

// bg gives fire-and-forget recorders a context detached from the request.
func bg() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// RecordActive marks the player active today (deduplicated via a Redis set).
func (s *Service) RecordActive(gameID, playerID string) {
	go func() {
		ctx, cancel := bg()
		defer cancel()
		day := s.dayKey(time.Now())
		k := s.key(gameID, day, "act")
		pipe := s.RDB.Pipeline()
		pipe.SAdd(ctx, k, playerID)
		pipe.Expire(ctx, k, counterTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("stats: record active failed: %v", err)
		}
	}()
}

func (s *Service) recordCounter(gameID, kind string) {
	go func() {
		ctx, cancel := bg()
		defer cancel()
		day := s.dayKey(time.Now())
		k := s.key(gameID, day, kind)
		pipe := s.RDB.Pipeline()
		pipe.Incr(ctx, k)
		pipe.Expire(ctx, k, counterTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("stats: record %s failed: %v", kind, err)
		}
	}()
}

// RecordLogin increments the aggregate counter and persists the player's
// local login day. The daily identity table is what makes exact D1/D7/D30
// cohorts possible; players.last_login_at only retains the most recent login.
func (s *Service) RecordLogin(gameID, playerID string) {
	s.recordCounter(gameID, "login")
	go func() {
		ctx, cancel := bg()
		defer cancel()
		_, err := s.DB.Exec(ctx,
			`INSERT INTO player_login_daily (game_id, player_id, login_date)
			 SELECT p.game_id, p.id, $3::date FROM players p
			 WHERE p.game_id=$1 AND p.id=$2
			 ON CONFLICT DO NOTHING`,
			gameID, playerID, s.dayKey(time.Now()))
		if err != nil {
			log.Printf("stats: record player login day failed: %v", err)
		}
	}()
}

func (s *Service) RecordNewPlayer(gameID string) { s.recordCounter(gameID, "new") }

type DayRow struct {
	Date       string `json:"date"`
	Active     int    `json:"active"`
	Logins     int    `json:"logins"`
	NewPlayers int    `json:"new_players"`
}

// live reads today's (or any recent day's) counters straight from Redis.
func (s *Service) live(ctx context.Context, gameID, day string) (DayRow, error) {
	row := DayRow{Date: day}
	active, err := s.RDB.SCard(ctx, s.key(gameID, day, "act")).Result()
	if err != nil {
		return row, err
	}
	row.Active = int(active)
	for kind, dst := range map[string]*int{"login": &row.Logins, "new": &row.NewPlayers} {
		v, err := s.RDB.Get(ctx, s.key(gameID, day, kind)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return row, err
		}
		*dst, _ = strconv.Atoi(v)
	}
	return row, nil
}

// Flush upserts today's and yesterday's Redis counters into Postgres for
// every game. Called periodically; loses nothing if it runs twice.
func (s *Service) Flush(ctx context.Context) {
	games, err := s.Games.List(ctx)
	if err != nil {
		log.Printf("stats: flush list games failed: %v", err)
		return
	}
	now := time.Now()
	for _, day := range []string{s.dayKeyOffset(now, -1), s.dayKey(now)} {
		for _, g := range games {
			row, err := s.live(ctx, g.ID, day)
			if err != nil {
				log.Printf("stats: flush read failed: %v", err)
				continue
			}
			if row.Active == 0 && row.Logins == 0 && row.NewPlayers == 0 {
				continue
			}
			_, err = s.DB.Exec(ctx,
				`INSERT INTO stats_daily (game_id, date, active, logins, new_players)
				 VALUES ($1, $2, $3, $4, $5)
				 ON CONFLICT (game_id, date) DO UPDATE SET
				   active = GREATEST(stats_daily.active, EXCLUDED.active),
				   logins = GREATEST(stats_daily.logins, EXCLUDED.logins),
				   new_players = GREATEST(stats_daily.new_players, EXCLUDED.new_players)`,
				g.ID, day, row.Active, row.Logins, row.NewPlayers)
			if err != nil {
				log.Printf("stats: flush write failed: %v", err)
			}
		}
	}
}

// StartAggregator flushes every 10 minutes until ctx ends.
func (s *Service) StartAggregator(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		defer ticker.Stop()
		s.Flush(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Flush(ctx)
			}
		}
	}()
}

// Query returns the last N days (oldest first), with today taken live from
// Redis so the console shows fresh numbers.
func (s *Service) Query(ctx context.Context, gameID string, days int) ([]DayRow, error) {
	now := time.Now()
	today := s.dayKey(now)
	since := s.dayKeyOffset(now, -(days - 1))

	rows, err := s.DB.Query(ctx,
		`SELECT to_char(date, 'YYYY-MM-DD'), active, logins, new_players
		 FROM stats_daily WHERE game_id = $1 AND date >= $2::date ORDER BY date`,
		gameID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]DayRow{}
	for rows.Next() {
		var r DayRow
		if err := rows.Scan(&r.Date, &r.Active, &r.Logins, &r.NewPlayers); err != nil {
			return nil, err
		}
		byDay[r.Date] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if liveRow, err := s.live(ctx, gameID, today); err == nil {
		stored := byDay[today]
		byDay[today] = DayRow{Date: today,
			Active:     max(liveRow.Active, stored.Active),
			Logins:     max(liveRow.Logins, stored.Logins),
			NewPlayers: max(liveRow.NewPlayers, stored.NewPlayers)}
	}

	out := make([]DayRow, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := s.dayKeyOffset(now, -i)
		row, ok := byDay[day]
		if !ok {
			row = DayRow{Date: day}
		}
		out = append(out, row)
	}
	return out, nil
}

// PlatformStats is the whole-platform view: the numbers the console shows
// before you have picked a game.
type PlatformStats struct {
	Totals struct {
		Games    int `json:"games"`
		Players  int `json:"players"`
		Accounts int `json:"accounts"`
		Online   int `json:"online"`
	} `json:"totals"`
	Today     DayRow     `json:"today"`
	Days      []DayRow   `json:"days"`
	Games     []GameStat `json:"games"`
	Retention Retention  `json:"retention"`
	Timezone  string     `json:"timezone"`
}

type GameStat struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Online     int    `json:"online"`
	Active     int    `json:"active"`
	Logins     int    `json:"logins"`
	NewPlayers int    `json:"new_players"`
	Trend      []int  `json:"trend"` // active players, oldest first
}

// Retention is derived from players.last_login_at, which only holds the most
// recent login — so it answers "did this cohort ever come back", not a strict
// day-N cohort. For yesterday's cohort the two coincide; for the 7-day cohort
// it is a return rate, and the console labels it that way.
type Retention struct {
	D1Cohort int `json:"d1_cohort"`
	D1Back   int `json:"d1_back"`
	D7Cohort int `json:"d7_cohort"`
	D7Back   int `json:"d7_back"`
}

// retention counts, per cohort day, how many of the players first seen that
// day have logged in again since.
func (s *Service) retention(ctx context.Context) (Retention, error) {
	var r Retention
	tz := s.Loc.String()
	err := s.DB.QueryRow(ctx,
		`WITH p AS (
		   SELECT (created_at AT TIME ZONE $1)::date    AS born,
		          (last_login_at AT TIME ZONE $1)::date AS seen
		   FROM players
		 )
		 SELECT
		   count(*) FILTER (WHERE born = (now() AT TIME ZONE $1)::date - 1),
		   count(*) FILTER (WHERE born = (now() AT TIME ZONE $1)::date - 1 AND seen > born),
		   count(*) FILTER (WHERE born = (now() AT TIME ZONE $1)::date - 7),
		   count(*) FILTER (WHERE born = (now() AT TIME ZONE $1)::date - 7 AND seen > born)
		 FROM p`, tz,
	).Scan(&r.D1Cohort, &r.D1Back, &r.D7Cohort, &r.D7Back)
	return r, err
}

// gameTrends returns each game's daily active counts since the given day.
func (s *Service) gameTrends(ctx context.Context, since string) (map[string]map[string]int, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT game_id::text, to_char(date, 'YYYY-MM-DD'), active
		 FROM stats_daily WHERE date >= $1::date`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var id, day string
		var active int
		if err := rows.Scan(&id, &day, &active); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]int{}
		}
		out[id][day] = active
	}
	return out, rows.Err()
}

// Platform aggregates every game's activity. Today comes from Redis (not yet
// flushed) and older days from Postgres, the same split Query uses per game.
func (s *Service) Platform(ctx context.Context, days int, online func(string) int) (*PlatformStats, error) {
	out := &PlatformStats{Timezone: s.Loc.String()}
	games, err := s.Games.List(ctx)
	if err != nil {
		return nil, err
	}
	out.Totals.Games = len(games)

	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM players`).Scan(&out.Totals.Players); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&out.Totals.Accounts); err != nil {
		return nil, err
	}

	now := time.Now()
	today := s.dayKey(now)
	since := s.dayKeyOffset(now, -(days - 1))

	// Stored history, summed across games.
	rows, err := s.DB.Query(ctx,
		`SELECT to_char(date, 'YYYY-MM-DD'), sum(active), sum(logins), sum(new_players)
		 FROM stats_daily WHERE date >= $1::date GROUP BY date ORDER BY date`, since)
	if err != nil {
		return nil, err
	}
	byDay := map[string]DayRow{}
	for rows.Next() {
		var r DayRow
		if err := rows.Scan(&r.Date, &r.Active, &r.Logins, &r.NewPlayers); err != nil {
			rows.Close()
			return nil, err
		}
		byDay[r.Date] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if out.Retention, err = s.retention(ctx); err != nil {
		return nil, err
	}
	// A week of per-game history powers the sparkline in the games table.
	trendFrom := s.dayKeyOffset(now, -6)
	trends, err := s.gameTrends(ctx, trendFrom)
	if err != nil {
		return nil, err
	}

	// Today's live counters, per game, so the per-game table and the platform
	// total stay consistent with each other.
	var liveToday DayRow
	for _, g := range games {
		live, err := s.live(ctx, g.ID, today)
		if err != nil {
			return nil, err
		}
		stored := DayRow{}
		if err := s.DB.QueryRow(ctx,
			`SELECT active, logins, new_players FROM stats_daily WHERE game_id = $1 AND date = $2::date`,
			g.ID, today).Scan(&stored.Active, &stored.Logins, &stored.NewPlayers); err != nil {
			stored = DayRow{} // no row yet today
		}
		gs := GameStat{
			ID: g.ID, Name: g.Name, Online: online(g.ID),
			Active:     max(live.Active, stored.Active),
			Logins:     max(live.Logins, stored.Logins),
			NewPlayers: max(live.NewPlayers, stored.NewPlayers),
		}
		gs.Trend = make([]int, 0, 7)
		for i := 6; i >= 0; i-- {
			day := s.dayKeyOffset(now, -i)
			v := trends[g.ID][day]
			if day == today {
				v = max(v, gs.Active) // today is still only in Redis
			}
			gs.Trend = append(gs.Trend, v)
		}
		out.Games = append(out.Games, gs)
		out.Totals.Online += gs.Online
		liveToday.Active += gs.Active
		liveToday.Logins += gs.Logins
		liveToday.NewPlayers += gs.NewPlayers
	}
	liveToday.Date = today
	byDay[today] = liveToday
	out.Today = liveToday

	out.Days = make([]DayRow, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := s.dayKeyOffset(now, -i)
		row, ok := byDay[day]
		if !ok {
			row = DayRow{Date: day}
		}
		out.Days = append(out.Days, row)
	}
	sort.Slice(out.Games, func(i, j int) bool { return out.Games[i].Active > out.Games[j].Active })
	return out, nil
}

type Handler struct {
	Svc    *Service
	Online func(gameID string) int // realtime hub online count
}

// PlatformStats handles GET /admin/api/stats?days=30.
func (h *Handler) PlatformStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	res, err := h.Svc.Platform(r.Context(), days, h.Online)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GameStats handles GET /admin/api/games/{id}/stats?days=30.
func (h *Handler) GameStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	gameID := r.PathValue("id")
	rows, err := h.Svc.Query(r.Context(), gameID, days)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"days":     rows,
		"online":   h.Online(gameID),
		"timezone": h.Svc.Loc.String(),
	})
}
