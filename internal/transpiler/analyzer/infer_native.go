package analyzer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The analyzer's expression inference on galop's native tree: the twin of
// inferPackageValInitType, which reads ANTLR's tree, for the initializers of
// the package-level bindings of a file collected on its native tree
// (extractPackageValsNative). TestValInferenceAgrees holds the pair together.

// inferPackageValInitTypeNative is inferPackageValInitType of expression e.
func (a *galaAnalyzer) inferPackageValInitTypeNative(e native.ExpressionNode, pkgName string, richAST *transpiler.RichAST) transpiler.Type {
	postfix := nativeSolePostfixExpr(e)
	if !postfix.Ok() {
		return signedNumberType(e.GetText())
	}
	primary := postfix.PrimaryExpr().Primary()
	if !primary.Ok() {
		return transpiler.NilType{}
	}
	suffixes := postfix.AllPostfixSuffix()
	if lit := primary.Literal(); lit.Ok() {
		if len(suffixes) > 0 {
			return transpiler.NilType{}
		}
		return nativeLiteralType(lit)
	}
	id := primary.Identifier()
	if !id.Ok() {
		return transpiler.NilType{}
	}

	var sel string
	if len(suffixes) > 0 {
		if s := suffixes[0].Identifier(); s.Ok() {
			sel = s.GetText()
		}
	}
	op, ok := a.packageValOperand(id.GetText(), sel)
	if !ok {
		return transpiler.NilType{}
	}
	if op.qualified {
		suffixes = suffixes[1:]
	}

	switch len(suffixes) {
	case 0: // a reference to another binding
		return packageValRefType(op, richAST)
	case 1:
	default:
		return transpiler.NilType{}
	}
	call := suffixes[0]
	if call.ChildCount() == 0 || !call.Child(0).IsToken() || call.Child(0).Terminal().GetText() != "(" {
		return transpiler.NilType{}
	}
	args := 0
	for range galaparser.NativeKids(call.ArgumentList().GalaNodeRef, native.GalaKIND_argument) {
		args++
	}
	return a.packageValCallType(op, args, pkgName, richAST)
}

// soleOperandChain is the precedence chain from an orExpr down to the
// unaryExpr that holds a postfix expression (or → and → equality →
// relational → additive → multiplicative → unary).
var soleOperandChain = [...]native.GalaKind{
	native.GalaKIND_andExpr,
	native.GalaKIND_equalityExpr,
	native.GalaKIND_relationalExpr,
	native.GalaKIND_additiveExpr,
	native.GalaKIND_multiplicativeExpr,
	native.GalaKIND_unaryExpr,
}

// nativeSolePostfixExpr is transformer.LeadingPostfixExpr(e, true): the
// postfix expression e is, or no node when e has a binary operator at any
// level or a unary operator before its operand.
func nativeSolePostfixExpr(e native.ExpressionNode) native.PostfixExprNode {
	n := e.OrExpr().GalaNodeRef
	for _, kind := range soleOperandChain {
		var ok bool
		if n, ok = galaparser.OnlyNativeKid(n, kind); !ok {
			return native.PostfixExprNode{}
		}
	}
	return native.UnaryExprNode{GalaNodeRef: n}.PostfixExpr()
}

// nativeLiteralType is literalType.
func nativeLiteralType(lit native.LiteralNode) transpiler.Type {
	switch {
	case lit.INT_LIT().Ok():
		return transpiler.BasicType{Name: "int"}
	case lit.FLOAT_LIT().Ok():
		return transpiler.BasicType{Name: "float64"}
	case lit.CHAR_LIT().Ok():
		return transpiler.BasicType{Name: "rune"}
	case lit.STRING().Ok(), lit.RAW_STRING().Ok(), lit.INTERPOLATED_STRING().Ok(), lit.FORMAT_STRING().Ok():
		return transpiler.BasicType{Name: "string"}
	}
	switch lit.GetText() {
	case "true", "false":
		return transpiler.BasicType{Name: "bool"}
	}
	return transpiler.NilType{}
}
