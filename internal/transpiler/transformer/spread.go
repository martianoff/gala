package transformer

import (
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
)

// extractArgExpression extracts the expression from a pattern in a function argument.
// It handles both ExpressionPatternContext (regular args) and RestPatternContext (spread args like x...).
// Returns the expression context, whether it's a spread argument, and an error if the pattern type is unsupported.
func extractArgExpression(pat grammar.IPatternContext) (grammar.IExpressionContext, bool, error) {
	if ep, ok := pat.(*grammar.ExpressionPatternContext); ok {
		return ep.Expression(), false, nil
	}
	if rp, ok := pat.(*grammar.RestPatternContext); ok {
		return rp.Expression(), true, nil
	}
	return nil, false, galaerr.NewSemanticError("only expressions allowed as function arguments")
}

// ellipsisPos returns a non-zero token.Pos if hasSpread is true, indicating variadic expansion.
func ellipsisPos(hasSpread bool) token.Pos {
	if hasSpread {
		return token.Pos(1)
	}
	return token.NoPos
}
