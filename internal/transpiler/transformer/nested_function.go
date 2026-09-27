package transformer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// checkNestedFunctionDeclaration rejects a named `func` declared inside a
// function body with GALA-E0052.
//
// The grammar admits a function declaration wherever a statement goes, but a
// local named function is not part of GALA — a local function is a lambda
// bound to a `val`. Go has no nested named functions either, so without this
// check the declaration was emitted verbatim inside the enclosing body and the
// generated file failed to parse, surfacing as the internal-error code E0017.
//
// The hint is shaped by what the declaration carries. A plain function maps
// one-to-one onto a lambda — `(params) Result => body` — so the hint spells out
// that lambda from the declaration's own signature. A lambda cannot take type
// parameters or default parameter values, and a method needs a receiver type,
// so those three shapes are sent to the top level instead.
//
// Each hint leads with a short clause ended by "; ": the caret annotation shows
// only the hint's first clause (galaerr's terseHint cuts at " (", "; " or ". "),
// and a signature's own " (" must not be where that cut lands.
func checkNestedFunctionDeclaration(ctx *grammar.FunctionDeclarationContext) error {
	name := ctx.Identifier().GetText()
	line, col := ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
	endCol := ctx.Identifier().GetStop().GetColumn() + len([]rune(name))

	sig := lambdaSignatureOf(ctx.Signature())
	kind, hint := "function", ""
	switch {
	case ctx.Receiver() != nil:
		kind = "method"
		hint = fmt.Sprintf("declare `%s` at the top level of the file; a method sits beside its receiver type", name)
	case ctx.TypeParameters() != nil:
		hint = fmt.Sprintf("declare `%s` at the top level of the file; a lambda cannot take type parameters", name)
	case sig.hasDefault:
		hint = fmt.Sprintf("declare `%s` at the top level of the file; a lambda cannot take default parameter values", name)
	case sig.partial:
		hint = "write it as a lambda bound to a `val`; a lambda names every parameter, as in `(x int) =>`"
	default:
		hint = fmt.Sprintf("write it as a lambda bound to a `val`; here, `val %s = %s => ...`", name, sig.text)
	}
	msg := fmt.Sprintf("%s `%s` is declared inside a function body", kind, name)
	return galaerr.NewCodedSemanticError(galaerr.CodeNestedFunctionDeclaration, line, col, msg, hint).WithSpan(endCol)
}

// lambdaSignature is a function signature respelled as a lambda's, for the
// hint: `(x int, y int) int`, on one line whatever the declaration's layout.
type lambdaSignature struct {
	text       string
	hasDefault bool // a parameter has a default value, which a lambda cannot carry
	partial    bool // a parameter lacks a name or a type: `(int)` reads as a name in a lambda
}

func lambdaSignatureOf(sig grammar.ISignatureContext) lambdaSignature {
	var out lambdaSignature
	var params []string
	if list := sig.Parameters().ParameterList(); list != nil {
		for _, p := range list.AllParameter() {
			out.hasDefault = out.hasDefault || p.ParamDefault() != nil
			out.partial = out.partial || p.Identifier() == nil || p.Type_() == nil
			params = append(params, oneLine(transpiler.SourceText(p)))
		}
	}
	out.text = "(" + strings.Join(params, ", ") + ")"
	if sig.Type_() != nil {
		out.text += " " + oneLine(transpiler.SourceText(sig.Type_()))
	}
	return out
}

// oneLine collapses every run of whitespace, newlines included, to one space.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
