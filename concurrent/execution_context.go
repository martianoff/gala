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
// Re-exported from go_interop so GALA code can build structured, cancellable
// computations without importing go_interop directly.

// CancelToken is an opaque cancellation handle observed by a Future's body.
type CancelToken = go_interop.CancelToken

// NewCancelToken creates a CancelToken that stays live until Cancel is called.
var NewCancelToken = go_interop.NewCancelToken

// NewTimeoutToken creates a CancelToken that auto-cancels after the duration.
var NewTimeoutToken = go_interop.NewTimeoutToken

// Cancel cancels a token, closing its CancelDone Signal.
var Cancel = go_interop.Cancel

// IsCancelled reports whether a token has been cancelled or timed out.
var IsCancelled = go_interop.IsCancelled

// CancelDone returns a Signal that closes when the token is cancelled.
var CancelDone = go_interop.CancelDone

// CancelErr returns the reason a token was cancelled, or nil if still live.
var CancelErr = go_interop.CancelErr

// WaitSignal blocks until a Signal (such as CancelDone) is closed.
var WaitSignal = go_interop.WaitSignal
