// Package safego runs background goroutines that cannot take the process
// down with them. A panic on the HTTP path is contained by net/http's
// per-connection recover; a panic in a bare `go func()` is not — it kills
// every game on the server.
package safego

import (
	"log"
	"runtime/debug"
)

// Go runs fn in a new goroutine, logging and swallowing any panic.
// name identifies the worker in the log line.
func Go(name string, fn func()) {
	go Run(name, fn)
}

// Run calls fn on the current goroutine with the same protection.
func Run(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in %s: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}
