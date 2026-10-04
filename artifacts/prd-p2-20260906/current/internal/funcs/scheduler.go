package funcs

import (
	"context"
	"log"
	"sync"
	"time"

	"minicloud/internal/repo"
	"minicloud/internal/safego"
)

const tickEvery = 10 * time.Second

// Scheduler fires cloud functions with cron_secs > 0. Timers live in process
// memory (single-instance assumption, same as rooms); after a restart every
// timer starts a fresh interval.
type Scheduler struct {
	Functions repo.Functions
	Runtime   *Runtime
	Logs      LogSink

	mu      sync.Mutex
	lastRun map[string]time.Time
}

func (s *Scheduler) Start(ctx context.Context) {
	s.lastRun = map[string]time.Time{}
	safego.Go("funcs.scheduler", func() {
		ticker := time.NewTicker(tickEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	})
}

func (s *Scheduler) tick(ctx context.Context) {
	fns, err := s.Functions.ListCron(ctx)
	if err != nil {
		log.Printf("funcs: cron scan failed: %v", err)
		return
	}
	now := time.Now()

	s.mu.Lock()
	// Drop timers for functions that were deleted or had their schedule
	// turned off, so the map cannot grow forever.
	live := make(map[string]struct{}, len(fns))
	for _, fn := range fns {
		live[fn.ID] = struct{}{}
	}
	for id := range s.lastRun {
		if _, ok := live[id]; !ok {
			delete(s.lastRun, id)
		}
	}

	due := make([]repo.CloudFunction, 0, len(fns))
	for _, fn := range fns {
		last, seen := s.lastRun[fn.ID]
		if !seen {
			// First sighting: start the interval now instead of treating the
			// zero time as "overdue", which would fire every cron job in
			// every game simultaneously on the first tick after a restart.
			s.lastRun[fn.ID] = now
			continue
		}
		if now.Sub(last) >= time.Duration(fn.CronSecs)*time.Second {
			s.lastRun[fn.ID] = now
			due = append(due, fn)
		}
	}
	s.mu.Unlock()

	for _, fn := range due {
		safego.Go("funcs.cron/"+fn.Name, func() { s.run(ctx, fn) })
	}
}

func (s *Scheduler) run(ctx context.Context, fn repo.CloudFunction) {
	// Derived from the scheduler context so runs stop at shutdown, with its
	// own deadline so one run cannot linger.
	runCtx, cancel := context.WithTimeout(ctx, execTimeout+time.Second)
	defer cancel()

	res, err := s.Runtime.Execute(runCtx, fn.GameID, &fn, Request{Method: "CRON"})
	if err != nil {
		log.Printf("funcs: cron %s/%s failed: %v", fn.GameID, fn.Name, err)
		if s.Logs != nil {
			s.Logs.Write(fn.GameID, "error", "funcs", fn.Name, "cron run failed: "+err.Error(), nil)
		}
		return
	}
	if s.Logs != nil { // cron output is otherwise invisible — keep it queryable
		s.Logs.Write(fn.GameID, "info", "funcs", fn.Name, "cron run ok",
			map[string]any{"logs": res.Logs, "result": res.Result})
	}
}
