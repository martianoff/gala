package concurrency

import (
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
	"martianoff/gala/internal/transpiler/scopewalk/nativewalk"
)

// The capture analysis on galop's native tree: each function here is the
// twin of the one it is named after, which reads ANTLR's tree, and walks the
// same scopes with the scope walk's native twin (nativewalk), which reports
// the same references at the same sites. TestCapturesAgree holds each pair
// together.

// FreeVariablesInLambdaNative is FreeVariablesInLambda of lambda.
func FreeVariablesInLambdaNative(lambda native.LambdaExpressionNode) []Capture {
	return capturesOf(func(w *scopewalk.Walker) { nativewalk.New(w, nil).Walk(lambda.GalaNodeRef) })
}

// FreeVariablesInExpressionNative is FreeVariablesInExpression of expr.
func FreeVariablesInExpressionNative(paramNames []string, expr native.ExpressionNode) []Capture {
	return capturesOf(func(w *scopewalk.Walker) {
		inThunkScope(w, paramNames, func() { nativewalk.New(w, nil).Walk(expr.GalaNodeRef) })
	})
}
