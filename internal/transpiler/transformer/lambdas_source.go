package transformer

import (
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// A lambda as its lowering reads it (lowerLambda): a lambdaExpression of
// ANTLR's tree (ctx) or of galop's native one (node), with readers that have
// an ANTLR half and a native half giving the same value for the same lambda,
// as the statement sources do (statements_source.go). A native lambda's
// block body is a native blockSrc, so its statements are lowered on the
// native tree too, and its result type is a native typeSrc. Its parameters
// (transformParameter) and an expression body are lowered on ANTLR's tree:
// the native readers cross to it with crossTo.
//
// Lambdas are reached from expressions and calls, which are still lowered on
// ANTLR's tree. So the ANTLR entry points (transformLambdaWithExpectedType,
// the Sendable capture check) take the lambda's node on the native tree when
// the walk reads it (nativeNodeOf).

// lambdaSrc is a lambda expression: its node when it is read on the native
// tree, and its context, which a lambda reached from ANTLR's tree keeps for
// the readers not ported yet (lambdaArgCallee, the Try thunk's identity). A
// lambda the native expression walk reaches (lowerPrimaryExprNative) has no
// context: it is lowered context-free, where neither reader runs.
type lambdaSrc struct {
	ctx  *grammar.LambdaExpressionContext
	node native.LambdaExpressionNode
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

// expression is l's expression body, and nil when its body is a block.
func (l lambdaSrc) expression(t *galaASTTransformer) grammar.IExpressionContext {
	if l.node.Ok() {
		if e := l.node.Expression(); e.Ok() {
			return crossTo[*grammar.ExpressionContext](t, e.GalaNodeRef)
		}
		return nil
	}
	return l.ctx.Expression()
}
