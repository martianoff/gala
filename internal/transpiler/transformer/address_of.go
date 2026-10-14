package transformer

import (
	"fmt"
	"go/ast"
	"strings"

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
	name, fixed := "", notFixed
	if postfix, ok := operandCtx.PostfixExpr().(*grammar.PostfixExprContext); ok && operandCtx.UnaryOp() == nil {
		name, fixed = t.immutablePostfixBinding(postfix)
	}
	var why, hint string
	switch {
	case fixed == fixedParam:
		why, hint = fmt.Sprintf("parameter %s is not declared `var`", name), addressHint(name, fmt.Sprintf("declare the parameter `var %s`", name))
	case fixed.isReceiver():
		why, hint = fmt.Sprintf("receiver %s is immutable", name), t.receiverAddressHint(name, fixed)
	case strings.Contains(name, "."):
		why, hint = fmt.Sprintf("%s is a val", name), addressHint(name, "copy it into a local `var`")
	case name != "":
		why, hint = fmt.Sprintf("%s is a val", name), addressHint(name, fmt.Sprintf("declare it `var %s`", name))
	default:
		why, hint = t.immutablePathReason(operand)
	}
	if why == "" {
		return nil
	}
	text := transpiler.SourceText(operandCtx)
	tok := ctx.GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeAddressOfImmutable, tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("cannot take the address of %s: %s", text, why), hint,
	).WithSpan(tok.GetColumn() + 1 + len([]rune(text)))
}

// receiverAddressHint is the hint for `&recv`: a pointer receiver is already
// the pointer, and a value receiver becomes one.
func (t *galaASTTransformer) receiverAddressHint(name string, fixed fixedBinding) string {
	if fixed == fixedPointerReceiver {
		return fmt.Sprintf("%s is already a pointer; pass %s itself", name, name)
	}
	typ := t.getValType(name)
	recv := "T"
	if !transpiler.IsUnusable(typ) {
		recv = displayType(typ)
	}
	return addressHint(name, fmt.Sprintf("declare a pointer receiver (%s *%s)", name, recv))
}

// addressHint is the hint for the address of the immutable binding name: how
// to make it addressable.
func addressHint(name, how string) string {
	return fmt.Sprintf("pass %s itself; %s where a pointer is needed", name, how)
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
	case *ast.CompositeLit:
		return "", "" // a fresh value
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
		x, ok := t.immutableGetOperand(e)
		if !ok {
			return "", ""
		}
		switch x := x.(type) {
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
