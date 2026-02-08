package concurrent

import (
	"martianoff/gala/go_interop"
	"time"
)

// ExecutionContext abstracts where/how async tasks execute.
// Re-exported from go_interop for convenience.
type ExecutionContext = go_interop.ExecutionContext

// UnboundedExecutionContext spawns a new goroutine for each task.
// This is the default ExecutionContext.
type UnboundedExecutionContext = go_interop.UnboundedExecutionContext

// FixedPoolExecutionContext executes tasks using a fixed-size worker pool.
type FixedPoolExecutionContext = go_interop.FixedPoolExecutionContext

// SingleThreadExecutionContext executes tasks sequentially in a single goroutine.
type SingleThreadExecutionContext = go_interop.SingleThreadExecutionContext

// GlobalEC returns the global default ExecutionContext.
var GlobalEC = go_interop.GlobalEC

// SetGlobalEC sets the global default ExecutionContext.
var SetGlobalEC = go_interop.SetGlobalEC

// NewFixedPoolEC creates a new FixedPoolExecutionContext with n workers.
var NewFixedPoolEC = go_interop.NewFixedPoolEC

// NewSingleThreadEC creates a new SingleThreadExecutionContext.
var NewSingleThreadEC = go_interop.NewSingleThreadEC

// Spawn starts a new goroutine executing the given function.
// Re-exported from go_interop for convenience with async operations.
var Spawn = go_interop.Spawn

// Sleep pauses the current goroutine for the given duration.
// Re-exported from go_interop for convenience with async operations.
func Sleep(d time.Duration) {
	go_interop.GoSleep(d)
}
