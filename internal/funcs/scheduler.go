package funcs

import (
	"context"
	"log"
	"time"

	"zekumo/internal/repo"
	"zekumo/internal/safego"
)

const tickEvery = 10 * time.Second

// Scheduler fires cloud functions with cron_secs > 0. Due times live in
// PostgreSQL. Replicas claim each due interval once, and restarts preserve due
// times. A claimed interval is not retried after a process crash.
type Scheduler struct {
	Functions repo.Functions
	Runtime   *Runtime
	Logs      LogSink
}

func (s *Scheduler) Start(ctx context.Context) {
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
	due, err := s.Functions.ClaimCron(ctx, 32)
	if err != nil {
		log.Printf("funcs: cron claim failed: %v", err)
		return
	}

	for _, fn := range due {
		safego.Go("funcs.cron/"+fn.Name, func() { s.run(ctx, fn) })
	}
}

func (s *Scheduler) run(ctx context.Context, fn repo.CloudFunction) {
	// Derived from the scheduler context so runs stop at shutdown, with its
	// own deadline so one run cannot linger.
	runCtx, cancel := context.WithTimeout(ctx, execTimeout+time.Second)
	defer cancel()

	start := time.Now()
	res, err := s.Runtime.Execute(runCtx, fn.GameID, &fn, Request{Method: "CRON"})
	if err != nil {
		log.Printf("funcs: cron %s/%s failed: %v", fn.GameID, fn.Name, err)
		if s.Logs != nil {
			s.Logs.Write(fn.GameID, "error", "funcs", fn.Name, "cron run failed: "+err.Error(), map[string]any{"revision": fn.Revision, "duration_ms": time.Since(start).Milliseconds()})
		}
		return
	}
	if s.Logs != nil { // cron output is otherwise invisible — keep it queryable
		s.Logs.Write(fn.GameID, "info", "funcs", fn.Name, "cron run ok",
			map[string]any{"logs": res.Logs, "result": res.Result, "revision": fn.Revision, "duration_ms": time.Since(start).Milliseconds()})
	}
}
