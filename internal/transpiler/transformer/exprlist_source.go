package transformer

import (
	"go/ast"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// exprListSrc is an expression list: a context on ANTLR's tree, or a node on
// the native one.
type exprListSrc struct {
	ctx  *grammar.ExpressionListContext
	node native.GalaNodeRef
}

// antlrExprList is the expression list ctx, on ANTLR's tree.
func antlrExprList(ctx *grammar.ExpressionListContext) exprListSrc { return exprListSrc{ctx: ctx} }

// nativeExprList is the expression list node n, which its reader requires
// (see nativeExpr).
func nativeExprList(n native.GalaNodeRef) exprListSrc {
	if !n.Ok() {
		// invariant: native-bridge — the walk reads only bridged trees.
		panic("native tree: no node, not an expression list")
	}
	return exprListSrc{node: n}
}

// exprs is l's expressions, in order.
func (l exprListSrc) exprs() []exprSrc {
	if l.node.Ok() {
		var out []exprSrc
		for e := range galaparser.NativeKids(l.node, native.GalaKIND_expression) {
			out = append(out, nativeExpr(e))
		}
		return out
	}
	all := l.ctx.AllExpression()
	out := make([]exprSrc, len(all))
	for i, e := range all {
		out[i] = antlrExpr(e)
	}
	return out
}

// lowerExprList lowers l's expressions, in order.
func (t *galaASTTransformer) lowerExprList(l exprListSrc) ([]ast.Expr, error) {
	return t.lowerExprs(l.exprs())
}

// lowerExprs lowers exprs, in order.
func (t *galaASTTransformer) lowerExprs(exprs []exprSrc) ([]ast.Expr, error) {
	out := make([]ast.Expr, 0, len(exprs))
	for _, e := range exprs {
		expr, err := t.lowerExpr(e)
		if err != nil {
			return nil, err
		}
		out = append(out, expr)
	}
	return out, nil
}
