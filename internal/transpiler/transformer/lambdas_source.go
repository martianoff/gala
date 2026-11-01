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
// native tree too; its result type is a native typeSrc, an expression body
// a native exprSrc, and its parameters are read there (lowerParam). An
// expression body of a lambda that returns nothing is lowered as an
// expression statement (transformVoidExpressionBody), on ANTLR's tree.
//
// The native walk reaches a lambda on its node: from an expression
// (lowerPrimaryExprNative), a call argument (argContent) or a slot-typed
// value (classifyExpr). Code still on ANTLR's tree enters through
// transformLambdaWithExpectedType, which takes the lambda's node when the
// walk reads it (lambdaOf).

// lambdaSrc is a lambda expression: its node when it is read on the native
// tree, its context when it is read on ANTLR's. A lambda reached from ANTLR's
// tree keeps its context alongside its node (lambdaOf); one the native walk
// reaches (lowerPrimaryExprNative, argContent) comes with its node only.
type lambdaSrc struct {
	ctx  *grammar.LambdaExpressionContext
	node native.LambdaExpressionNode
}

// ok reports whether l is a lambda, not the zero lambdaSrc.
func (l lambdaSrc) ok() bool { return l.ctx != nil || l.node.Ok() }

// is reports whether l and o are the same lambda: the same node on the
// native tree, or the same context on ANTLR's. The two can meet: a file
// walked natively reads a lambda with its node only, and one in a default or
// an interpolation that falls back to ANTLR's tree (parseExprAt) with its
// context only, as no node of the file's tree stands for it (lambdaOf). Such
// two are different lambdas, and neither pair matches them.
func (l lambdaSrc) is(o lambdaSrc) bool {
	return (l.node.Ok() && l.node == o.node) || (l.ctx != nil && l.ctx == o.ctx)
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
		return readParamsNative(l.node.Parameters())
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
