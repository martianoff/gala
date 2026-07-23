package concurrent

import (
	"martianoff/gala/go_interop"
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

// === Cancellation ===
// Re-exported from go_interop so GALA code can build cancellable computations
// without importing go_interop directly. Cancellation is API-level: a Future
// carries a token internally; user code triggers it via Future.Cancel /
// WithTimeout and never handles a token itself.

// CancelToken is the opaque cancellation handle a Future carries internally.
type CancelToken = go_interop.CancelToken

// NewCancelToken creates a CancelToken that stays live until Cancel is called.
var NewCancelToken = go_interop.NewCancelToken

// Cancel cancels a token.
var Cancel = go_interop.Cancel

// IsCancelled reports whether a token has been cancelled.
var IsCancelled = go_interop.IsCancelled
