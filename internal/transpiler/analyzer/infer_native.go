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
	postfix := galaparser.NativeSolePostfixExpr(e)
	if !postfix.Ok() {
		return signedNumberType(e.GetText())
	}
	if len(postfix.AllCaseClause()) > 0 {
		return transpiler.NilType{} // a trailing match: typed by its arms
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
	if op.qualified() {
		suffixes = suffixes[1:]
	}

	switch len(suffixes) {
	case 0: // a reference to another binding
		return packageValRefType(op, richAST)
	case 1:
	default:
		return transpiler.NilType{}
	}
	// A postfix suffix spans a token or more: its first is a call's `(`.
	call := suffixes[0]
	if galaparser.NativeStartText(call.GalaNodeRef) != "(" {
		return transpiler.NilType{}
	}
	args := 0
	for range galaparser.NativeKids(call.ArgumentList().GalaNodeRef, native.GalaKIND_argument) {
		args++
	}
	return a.packageValCallType(op, args, pkgName, richAST)
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
	case lit.GetText() == "true", lit.GetText() == "false":
		return transpiler.BasicType{Name: "bool"}
	}
	return transpiler.NilType{}
}
