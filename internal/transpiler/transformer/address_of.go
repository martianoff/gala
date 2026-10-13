package transformer

import (
	"fmt"
	"go/ast"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Taking the address of an immutable value
//
// `&x` yields a *T, and a *T writes. A val, a parameter not declared `var`, a
// receiver, an immutable field and a field reached through a val cannot be
// changed, so none of them has an address (GALA-E0076): the value is passed
// itself, or the binding is declared `var` where Go needs a *T.

// immutableAddressError reports `&operand` when operand is immutable, or nil.
// operand is the operand's lowering, used for the field paths a bare name
// does not cover.
func (t *galaASTTransformer) immutableAddressError(ctx *grammar.UnaryExprContext, operandCtx *grammar.UnaryExprContext, operand ast.Expr) error {
	text := transpiler.SourceText(operandCtx)
	var why, hint string
	if name := simpleIdentifierName(operandCtx); name != "" {
		switch {
		case t.isVal(name):
			why = fmt.Sprintf("%s is a val", name)
			hint = fmt.Sprintf("pass %s itself; declare it `var %s` where a pointer is needed", name, name)
		case t.fixedBindingOf(name) == fixedParam:
			why = fmt.Sprintf("parameter %s is not declared `var`", name)
			hint = fmt.Sprintf("pass %s itself; declare the parameter `var %s` where a pointer is needed", name, name)
		case t.fixedBindingOf(name) == fixedReceiver:
			why = fmt.Sprintf("receiver %s is immutable", name)
			hint = fmt.Sprintf("pass %s itself; declare a pointer receiver (%s *T) where a pointer is needed", name, name)
		}
	} else {
		why, hint = t.immutablePathReason(operand)
	}
	if why == "" {
		return nil
	}
	tok := ctx.GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeAddressOfImmutable, tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("cannot take the address of %s: %s", text, why), hint,
	).WithSpan(tok.GetColumn() + 1 + len([]rune(text)))
}

// immutableFieldWriteError reports a write (`o.n = v`, `o.n++`) to a field
// that is immutable or reached through a val, or returns nil. target is the
// lowered target; a bare name is checked by immutableWriteError.
func (t *galaASTTransformer) immutableFieldWriteError(targetCtx antlr.ParserRuleContext, target ast.Expr, verb string) error {
	if _, bare := target.(*ast.Ident); bare {
		return nil
	}
	why, hint := t.immutablePathReason(target)
	if why == "" {
		return nil
	}
	err := t.semanticErrorAt(targetCtx, fmt.Sprintf("cannot %s %s: %s", verb, transpiler.SourceText(targetCtx), why))
	err.Hint = hint
	return err
}

// immutablePathReason says why a lowered field path is immutable — it
// reaches an immutable field, or a val, before any pointer — with a hint, or
// returns "" when it is addressable and writable. A field of a pointee is
// always writable, so the walk stops at the first pointer, and at a type it
// cannot resolve, which may be one.
func (t *galaASTTransformer) immutablePathReason(expr ast.Expr) (why, hint string) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return t.immutablePathReason(e.X)
	case *ast.SelectorExpr:
		xType := t.getExprTypeName(e.X)
		if _, isPtr := xType.(transpiler.PointerType); isPtr || transpiler.IsUnusable(xType) {
			return "", ""
		}
		return t.immutablePathReason(e.X)
	case *ast.CallExpr:
		// `.Get()` is how a val, an immutable field or an imported val is read.
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != transpiler.MethodGet || len(e.Args) != 0 || !t.isImmutableOperand(sel.X) {
			return "", ""
		}
		switch x := sel.X.(type) {
		case *ast.Ident:
			return fmt.Sprintf("it is reached through val %s", x.Name),
				fmt.Sprintf("declare it `var %s`", x.Name)
		case *ast.SelectorExpr:
			if pkg, ok := x.X.(*ast.Ident); ok && t.importedPackageVal(pkg.Name, x.Sel.Name) != nil {
				return fmt.Sprintf("it is reached through val %s.%s", pkg.Name, x.Sel.Name),
					"copy it into a local `var`"
			}
			return fmt.Sprintf("field %s is immutable", x.Sel.Name),
				fmt.Sprintf("declare the field `var %s`", x.Sel.Name)
		}
		return "it is immutable", "copy it into a local `var`"
	}
	if t.isImmutableType(t.getExprTypeName(expr)) {
		return "it is immutable", "copy it into a local `var`"
	}
	return "", ""
}
