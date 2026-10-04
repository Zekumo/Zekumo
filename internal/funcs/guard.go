package funcs

import (
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// ErrBusy is returned when every execution slot is taken.
var ErrBusy = errors.New("too many cloud functions running, try again shortly")

const (
	maxConcurrent = 4
	acquireWait   = 2 * time.Second
	heapPollEvery = 250 * time.Millisecond
)

// guard bounds what tenant scripts can cost the process. This version of goja
// has no per-VM memory cap, so the levers available are: run few VMs at once,
// and interrupt every live VM if the process heap crosses a ceiling.
//
// Residual risk: a single allocating builtin (`'x'.repeat(5e8)`) completes
// without ever reaching an interrupt checkpoint, so the ceiling can be
// overshot by one such allocation. Bounding concurrency is what keeps that
// from multiplying into an OOM.
type guard struct {
	sem       chan struct{}
	heapLimit uint64

	mu     sync.Mutex
	active map[*goja.Runtime]struct{}
	poller bool
}

func newGuard(heapLimitBytes uint64) *guard {
	return &guard{
		sem:       make(chan struct{}, maxConcurrent),
		heapLimit: heapLimitBytes,
		active:    map[*goja.Runtime]struct{}{},
	}
}

// acquire takes an execution slot, waiting briefly before giving up.
func (g *guard) acquire() error {
	timer := time.NewTimer(acquireWait)
	defer timer.Stop()
	select {
	case g.sem <- struct{}{}:
		return nil
	case <-timer.C:
		return ErrBusy
	}
}

func (g *guard) release() { <-g.sem }

// watch registers a VM so the heap poller can interrupt it, starting the
// poller if this is the first live VM. Idle servers poll nothing.
func (g *guard) watch(vm *goja.Runtime) func() {
	g.mu.Lock()
	g.active[vm] = struct{}{}
	if !g.poller {
		g.poller = true
		go g.pollHeap()
	}
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		delete(g.active, vm)
		g.mu.Unlock()
	}
}

func (g *guard) pollHeap() {
	ticker := time.NewTicker(heapPollEvery)
	defer ticker.Stop()
	for range ticker.C {
		g.mu.Lock()
		if len(g.active) == 0 {
			g.poller = false
			g.mu.Unlock()
			return
		}
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		if stats.HeapAlloc > g.heapLimit {
			for vm := range g.active {
				vm.Interrupt("memory limit exceeded")
			}
		}
		g.mu.Unlock()
	}
}
