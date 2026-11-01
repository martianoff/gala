package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// A lambda as its lowering reads it (lowerLambda): a lambdaExpression of
// ANTLR's tree (ctx) or of galop's native one (node), with readers that have
// an ANTLR half and a native half giving the same value for the same lambda,
// as the statement sources do (statements_source.go). A native lambda's
// block body is a native blockSrc, so its statements are lowered on the
// native tree too; its result type is a native typeSrc and an expression
// body a native exprSrc. An expression body of a lambda that returns nothing
// is lowered as an expression statement (transformVoidExpressionBody), on
// ANTLR's tree.
// Its parameters (transformParameter) are lowered on ANTLR's tree too: the
// native readers cross to it with crossTo.
//
// The native walk reaches a lambda on its node: from an expression
// (lowerPrimaryExprNative), a call argument (argContent) or a slot-typed
// value (classifyExpr). Code still on ANTLR's tree enters through
// transformLambdaWithExpectedType, which takes the lambda's node when the
// walk reads it (lambdaOf).

// lambdaSrc is a lambda expression: its node when it is read on the native
// tree, and its context, which a lambda reached from ANTLR's tree keeps for
// the readers not ported yet (its parameters). A lambda the native walk
// reaches (lowerPrimaryExprNative, argContent) comes with its node only;
// those readers cross for its context (antlr).
type lambdaSrc struct {
	ctx  *grammar.LambdaExpressionContext
	node native.LambdaExpressionNode
}

// antlr is l's context on ANTLR's tree, crossing from its node when l came
// without one.
func (l lambdaSrc) antlr(t *galaASTTransformer) *grammar.LambdaExpressionContext {
	if l.ctx == nil && l.node.Ok() {
		return crossTo[*grammar.LambdaExpressionContext](t, l.node.GalaNodeRef)
	}
	return l.ctx
}

// ok reports whether l is a lambda, not the zero lambdaSrc.
func (l lambdaSrc) ok() bool { return l.ctx != nil || l.node.Ok() }

// is reports whether l and o are the same lambda: the same node on the
// native tree, the same context on ANTLR's. A lambda the walk reads on the
// native tree always has its node (lambdaOf), so the two never mix.
func (l lambdaSrc) is(o lambdaSrc) bool {
	if l.node.Ok() || o.node.Ok() {
		return l.node == o.node
	}
	return l.ctx != nil && l.ctx == o.ctx
}

// lambdaOf is the lambda ctx, on the native tree when the walk reads it.
func (t *galaASTTransformer) lambdaOf(ctx *grammar.LambdaExpressionContext) lambdaSrc {
	l := lambdaSrc{ctx: ctx}
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_lambdaExpression); ok {
		l.node = native.LambdaExpressionNode{GalaNodeRef: n}
	}
	return l
}

// at is where l is written.
func (l lambdaSrc) at() srcNode {
	if l.node.Ok() {
		return srcNode{node: l.node.GalaNodeRef}
	}
	return srcNode{ctx: l.ctx}
}

// params is l's parameters, in order.
func (l lambdaSrc) params(t *galaASTTransformer) []paramDecl {
	if l.node.Ok() {
		return t.readParamsNative(l.node.Parameters())
	}
	return readParams(l.ctx.Parameters().(*grammar.ParametersContext))
}

// paramCount is the number of parameters l declares.
func (l lambdaSrc) paramCount() int {
	if l.node.Ok() {
		n := 0
		for range galaparser.NativeKids(l.node.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
			n++
		}
		return n
	}
	params, ok := l.ctx.Parameters().(*grammar.ParametersContext)
	if !ok || params.ParameterList() == nil {
		return 0
	}
	return len(params.ParameterList().(*grammar.ParameterListContext).AllParameter())
}

// resultType is l's declared result type, and no type when it declares none.
func (l lambdaSrc) resultType() typeSrc {
	if l.node.Ok() {
		if typ := l.node.Type_(); typ.Ok() {
			return nativeType(typ)
		}
		return typeSrc{}
	}
	return antlrType(l.ctx.Type_())
}

// block is l's block body, and the zero blockSrc when its body is an
// expression.
func (l lambdaSrc) block() blockSrc {
	if l.node.Ok() {
		return blockSrc{node: l.node.Block().GalaNodeRef}
	}
	b, _ := l.ctx.Block().(*grammar.BlockContext)
	return blockSrc{ctx: b}
}

// expression is l's expression body, and the zero exprSrc when its body is a
// block.
func (l lambdaSrc) expression(t *galaASTTransformer) exprSrc {
	if l.node.Ok() {
		if e := l.node.Expression(); e.Ok() {
			return nativeExpr(e.GalaNodeRef)
		}
		return exprSrc{}
	}
	return t.exprOf(l.ctx.Expression())
}

// at is where f is written.
func (f partialSrc) at() srcNode {
	if f.node.Ok() {
		return srcNode{node: f.node}
	}
	return srcNode{ctx: f.ctx}
}

// cases is f's case clauses, in order. Those read on the native tree are
// reported to the onNativeCase hook, as a match's are (matchCases).
func (f partialSrc) cases(t *galaASTTransformer) []caseSrc {
	if !f.node.Ok() {
		return antlrCases(f.ctx.AllCaseClause())
	}
	var out []caseSrc
	for c := range galaparser.NativeKids(f.node, native.GalaKIND_caseClause) {
		if t.onNativeCase != nil {
			t.onNativeCase(c.Index(), int(c.Start().Line))
		}
		out = append(out, caseSrc{node: c})
	}
	return out
}
