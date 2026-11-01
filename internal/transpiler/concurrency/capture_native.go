package concurrency

import (
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
	"martianoff/gala/internal/transpiler/scopewalk/nativewalk"
)

// The capture analysis on galop's native tree: each function walks the
// closure's scopes with the scope walk on the native tree (nativewalk).

// FreeVariablesInLambdaNative returns the free variables captured by an
// explicit lambda. The lambda's declared parameters are bound (never
// captured); its body may be an expression (`(x) => x + n`) or a block
// (`(x) => { ... }`).
func FreeVariablesInLambdaNative(lambda native.LambdaExpressionNode) []Capture {
	return capturesOf(func(w *scopewalk.Walker) { nativewalk.New(w, nil).Walk(lambda.GalaNodeRef) })
}

// FreeVariablesInExpressionNative returns the free variables of a bare
// expression — the shape a by-name / thunk argument desugars to.
// `Future(counter + 1)` is analyzed by calling this with the expression
// `counter + 1` and no params, which is identical to analyzing the explicit
// thunk `() => counter + 1`.
//
// paramNames lets a caller seed additional bound names (e.g. an implicit
// parameter of the thunk); pass nil/empty for the common Future/Spawn thunk.
func FreeVariablesInExpressionNative(paramNames []string, expr native.ExpressionNode) []Capture {
	return capturesOf(func(w *scopewalk.Walker) {
		inThunkScope(w, paramNames, func() { nativewalk.New(w, nil).Walk(expr.GalaNodeRef) })
	})
}
