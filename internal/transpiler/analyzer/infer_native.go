package analyzer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The analyzer's expression inference on galop's native tree, for the
// initializers of a file's package-level bindings (extractPackageValsNative).

// inferPackageValInitTypeNative determines the element type of a
// package-level binding from its initializer e, for the shapes the collected
// metadata settles without full expression inference:
//
//   - a literal (`3`, `-3`, `-0x10`, `"x"`, `true`, `s"..."`);
//   - a reference to another package-level binding (`Other`, `pkg.Other`);
//   - a non-generic constructor or function call (`Name(...)`, `pkg.Name(...)`):
//     a struct literal, a companion's (e.g. a sealed variant's) or the struct's
//     own Apply result, or a function's declared return type.
//
// Anything else — including a void call, an operator expression (`E + 1`)
// and a trailing match, which its arms type — yields NilType. That still unwraps
// correctly at a use site (which needs only the val/var classification) but
// weakens downstream inference, e.g. which fields of an imported struct-typed
// val are Immutable. A reference to a binding declared later in the same file
// is not resolved.
//
// A qualifier resolves through the declaring file's import table
// (a.currentQualifiers), and the operand and its type are settled by
// packageValOperand, packageValRefType and packageValCallType.
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

// nativeLiteralType is the type of a literal, by its token; NilType for `nil`.
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
