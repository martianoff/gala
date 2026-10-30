package parser

import "martianoff/gala/internal/parser/native"

// The functions here read the shape of an expression on galop's native
// tree, for the compiler's native-tree readers.

// soleOperandChain is the precedence chain from an orExpr down to the
// unaryExpr that holds a postfix expression.
var soleOperandChain = [...]native.GalaKind{
	native.GalaKIND_andExpr,
	native.GalaKIND_equalityExpr,
	native.GalaKIND_relationalExpr,
	native.GalaKIND_additiveExpr,
	native.GalaKIND_multiplicativeExpr,
	native.GalaKIND_unaryExpr,
}

// NativeSolePostfixExpr is the transformer's LeadingPostfixExpr(e, true) on
// the native tree: the postfix expression e is, or no node when e has a
// binary operator at any level (or → and → equality → relational → additive
// → multiplicative → unary) or a unary operator before its operand.
func NativeSolePostfixExpr(e native.ExpressionNode) native.PostfixExprNode {
	n := e.OrExpr().GalaNodeRef
	for _, kind := range soleOperandChain {
		var ok bool
		if n, ok = OnlyNativeKid(n, kind); !ok {
			return native.PostfixExprNode{}
		}
	}
	return native.UnaryExprNode{GalaNodeRef: n}.PostfixExpr()
}
