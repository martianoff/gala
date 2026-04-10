package transformer

import (
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
)

// extractArgExpression extracts the expression from a pattern in a function argument.
// It handles both ExpressionPatternContext (regular args) and RestPatternContext (spread args like x...).
// Returns the expression context, whether it's a spread argument, and an error if the pattern type is unsupported.
// NOTE: After FIX-050, an argument may be a direct lambdaExpression instead of a pattern.
// In that case pat is nil — callers should check arg.LambdaExpression() first.
func extractArgExpression(pat grammar.IPatternContext, line, col int) (grammar.IExpressionContext, bool, error) {
	if pat == nil {
		return nil, false, galaerr.NewSemanticErrorAt(line, col, "only expressions allowed as function arguments")
	}
	if ep, ok := pat.(*grammar.ExpressionPatternContext); ok {
		return ep.Expression(), false, nil
	}
	if rp, ok := pat.(*grammar.RestPatternContext); ok {
		return rp.Expression(), true, nil
	}
	return nil, false, galaerr.NewSemanticErrorAt(pat.GetStart().GetLine(), pat.GetStart().GetColumn(), "only expressions allowed as function arguments")
}

// extractArgContent extracts the expression or lambda from an ArgumentContext.
// After FIX-050, an argument may contain either a pattern (with an expression) or a direct lambdaExpression.
// Returns: exprCtx (may be nil for direct lambda), lambdaCtx (non-nil for direct lambda), isSpread, error.
func extractArgContent(arg *grammar.ArgumentContext) (grammar.IExpressionContext, *grammar.LambdaExpressionContext, bool, error) {
	// Check for direct lambda first (FIX-050: lambdaExpression alternative in argument rule)
	if lambdaCtx := arg.LambdaExpression(); lambdaCtx != nil {
		return nil, lambdaCtx.(*grammar.LambdaExpressionContext), false, nil
	}
	pat := arg.Pattern()
	exprCtx, isSpread, err := extractArgExpression(pat, arg.GetStart().GetLine(), arg.GetStart().GetColumn())
	return exprCtx, nil, isSpread, err
}

// ellipsisPos returns a non-zero token.Pos if hasSpread is true, indicating variadic expansion.
func ellipsisPos(hasSpread bool) token.Pos {
	if hasSpread {
		return token.Pos(1)
	}
	return token.NoPos
}
