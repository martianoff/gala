package analyzer

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/transformer"
)

// The readers of ANTLR's tree the analyzer still runs where it reads a file
// on that tree. The transformer reads the native tree only.

// leadingPostfixExpr walks the precedence chain (or → and → equality →
// relational → additive → multiplicative → unary) down to the postfix
// expression of ctx's first operand, or nil when a unary operator or no
// operand intervenes. With sole set, every level must hold exactly one
// operand, i.e. ctx has no binary operator at all.
func leadingPostfixExpr(ctx grammar.IExpressionContext, sole bool) *grammar.PostfixExprContext {
	addExpr := leadingAdditiveExpr(ctx, sole)
	if addExpr == nil {
		return nil
	}
	fits := func(n int) bool { return n > 0 && (!sole || n == 1) }
	mulExprs := addExpr.AllMultiplicativeExpr()
	if !fits(len(mulExprs)) {
		return nil
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if !fits(len(unaryExprs)) {
		return nil
	}
	postfix, _ := unaryExprs[0].(*grammar.UnaryExprContext).PostfixExpr().(*grammar.PostfixExprContext)
	return postfix
}

// leadingAdditiveExpr walks the precedence chain of ctx down to the additive
// expression of its first operand, as leadingPostfixExpr does. With sole set,
// no `||`, `&&` or comparison may join several operands above it.
func leadingAdditiveExpr(ctx grammar.IExpressionContext, sole bool) *grammar.AdditiveExprContext {
	if ctx == nil {
		return nil
	}
	fits := func(n int) bool { return n > 0 && (!sole || n == 1) }
	orExpr, _ := ctx.OrExpr().(*grammar.OrExprContext)
	if orExpr == nil {
		return nil
	}
	andExprs := orExpr.AllAndExpr()
	if !fits(len(andExprs)) {
		return nil
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if !fits(len(eqExprs)) {
		return nil
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if !fits(len(relExprs)) {
		return nil
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if !fits(len(addExprs)) {
		return nil
	}
	addExpr, _ := addExprs[0].(*grammar.AdditiveExprContext)
	return addExpr
}

// primaryOf returns the primary a postfix expression starts from, or nil when
// it starts from a lambda, if-expression or partial function instead.
func primaryOf(postfix *grammar.PostfixExprContext) *grammar.PrimaryContext {
	if postfix == nil {
		return nil
	}
	primaryExpr, _ := postfix.PrimaryExpr().(*grammar.PrimaryExprContext)
	if primaryExpr == nil {
		return nil
	}
	primary, _ := primaryExpr.Primary().(*grammar.PrimaryContext)
	return primary
}

// checkGeneratedCodecNames reports the first identifier of tree spelled like
// a name the codec generator emits (transformer.IsGeneratedCodecName).
func checkGeneratedCodecNames(tree antlr.Tree) error {
	var found *grammar.IdentifierContext
	var visit func(antlr.Tree)
	visit = func(node antlr.Tree) {
		if found != nil {
			return
		}
		if id, ok := node.(*grammar.IdentifierContext); ok {
			if transformer.IsGeneratedCodecName(id.GetText()) {
				found = id
			}
			return
		}
		for _, child := range node.GetChildren() {
			visit(child)
		}
	}
	visit(tree)
	if found == nil {
		return nil
	}
	return transformer.GeneratedCodecNameError(found.GetText(), transpiler.PosFromToken(found.GetStart()))
}
