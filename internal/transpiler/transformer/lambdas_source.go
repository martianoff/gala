package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// A lambda as its lowering reads it (lowerLambda): a lambdaExpression node of
// the native tree. Its block body is a blockSrc, its result type a typeSrc, an
// expression body an exprSrc, and its parameters are read by lowerParam. An
// expression body of a lambda that returns nothing is lowered as an
// expression statement (transformVoidExpressionBody).
//
// The walk reaches a lambda from an expression (lowerPrimaryExprNative), a
// call argument (argContent) or a slot-typed value (classifyExpr).

// lambdaSrc is a lambda expression node.
type lambdaSrc struct {
	node native.LambdaExpressionNode
}

// ok reports whether l is a lambda, not the zero lambdaSrc.
func (l lambdaSrc) ok() bool { return l.node.Ok() }

// is reports whether l and o are the same lambda.
func (l lambdaSrc) is(o lambdaSrc) bool {
	return l.node.Ok() && l.node == o.node
}

// at is where l is written.
func (l lambdaSrc) at() srcNode { return srcNode{node: l.node.GalaNodeRef} }

// params is l's parameters, in order.
func (l lambdaSrc) params(t *galaASTTransformer) []paramDecl {
	return readParamsNative(l.node.Parameters())
}

// paramCount is the number of parameters l declares.
func (l lambdaSrc) paramCount() int {
	n := 0
	for range galaparser.NativeKids(l.node.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		n++
	}
	return n
}

// resultType is l's declared result type, and no type when it declares none.
func (l lambdaSrc) resultType() typeSrc {
	if typ := l.node.Type_(); typ.Ok() {
		return nativeType(typ)
	}
	return typeSrc{}
}

// block is l's block body, and the zero blockSrc when its body is an
// expression.
func (l lambdaSrc) block() blockSrc {
	return blockSrc{node: l.node.Block().GalaNodeRef}
}

// expression is l's expression body, and the zero exprSrc when its body is a
// block.
func (l lambdaSrc) expression(t *galaASTTransformer) exprSrc {
	if e := l.node.Expression(); e.Ok() {
		return nativeExpr(e.GalaNodeRef)
	}
	return exprSrc{}
}
