package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A val's address in an interface slot
//
// `&x` of a `val` (and of a parameter or receiver, which are immutable too) is
// a read-only `ConstPtr[T]`, not a `*T`: the pointer must not be a way to
// modify the value. ConstPtr has only its own methods (Deref, IsNil), so it
// does not implement an interface whose methods are declared on T or *T — the
// usual shape of an io.Writer-like or callback interface, whose methods have
// pointer receivers. Passed where such an interface is expected, the call used
// to reach Go, which rejected it as
//
//	cannot use std.NewConstPtr(c.Ptr()) (value of struct type std.ConstPtr[Counter])
//	as Notifier value in argument to send: std.ConstPtr[Counter] does not
//	implement Notifier (missing method Notify)
//
// naming a wrapper the author never wrote. The check names it in GALA: what the
// value is, why, and which binding to change.

// checkConstPtrInterface rejects a ConstPtr value filling a slot of interface
// type that ConstPtr does not implement (GALA-E0070). An empty interface
// (`any`), a non-interface slot and an unresolved type are left alone.
func (t *galaASTTransformer) checkConstPtrInterface(expr ast.Expr, slotType transpiler.Type, exprCtx antlr.ParserRuleContext) error {
	// The check runs for every typed slot, so the value is first tested
	// cheaply for being a ConstPtr at all.
	if slotType == nil || transpiler.IsUnusable(slotType) || slotType.IsAny() || !t.mayBeConstPtr(expr) {
		return nil
	}
	required, isIface := t.interfaceMethodNames(slotType)
	if !isIface || len(required) == 0 {
		return nil
	}
	actual := t.probeExprType(expr)
	if actual == nil || transpiler.IsUnusable(actual) || !t.isConstPtrType(actual) {
		return nil
	}
	cpMeta := t.getTypeMeta(actual.BaseName())
	if cpMeta == nil {
		return nil
	}
	missing := t.missingInterfaceMethods(cpMeta, required)
	if len(missing) == 0 {
		return nil
	}
	text := transpiler.SourceText(exprCtx)
	iface := displayType(slotType)
	tok := exprCtx.GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeConstPtrNotInterface, tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("cannot use %s (%s) as %s: a read-only ConstPtr does not implement %s (missing %s)",
			text, displayType(actual), iface, iface, strings.Join(missing, ", ")),
		t.constPtrInterfaceHint(actual, required, text),
	).WithSpan(tok.GetColumn() + len([]rune(text)))
}

// mayBeConstPtr is a cheap pre-filter for checkConstPtrInterface: expr is the
// lowering of `&x` (a NewConstPtr call), or a variable or call whose type
// could be a ConstPtr. A literal, an operator expression or a selector of a
// field is never one worth inferring.
func (t *galaASTTransformer) mayBeConstPtr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return true // `&x` lowers to a NewConstPtr call; any call may return one
	case *ast.Ident:
		return t.isConstPtrType(t.getType(e.Name))
	}
	return false
}

// constPtrInterfaceHint names the way out. When T's own value-receiver methods
// cover the interface, the value itself implements it and is passed as is;
// otherwise only a *T does, and a *T comes from the address of a `var`.
func (t *galaASTTransformer) constPtrInterfaceHint(constPtr transpiler.Type, required []string, text string) string {
	// Only `&name` names a binding that can be redeclared `var`; `&s.inner`
	// and other operands get the general hint.
	operand, isAddr := strings.CutPrefix(text, "&")
	isAddr = isAddr && token.IsIdentifier(operand)
	elem := "T"
	if gen, ok := constPtr.(transpiler.GenericType); ok && len(gen.Params) == 1 {
		elem = displayType(gen.Params[0])
		if meta := t.getTypeMeta(gen.Params[0].BaseName()); meta != nil && isAddr && valueReceiversCover(meta, required) {
			return fmt.Sprintf("%s implements it with value receivers: pass %s itself", elem, operand)
		}
	}
	if isAddr && t.fixedBindingOf(operand) == fixedReceiver {
		return fmt.Sprintf("&%s is read-only because a receiver is immutable; declare the receiver as a pointer (%s *%s) to pass it", operand, operand, elem)
	}
	if isAddr {
		return fmt.Sprintf("&%s is read-only because %s is immutable; declare it `var %s` to pass a *%s", operand, operand, operand, elem)
	}
	return fmt.Sprintf("a ConstPtr is read-only; pass the address of a `var` (a *%s) instead", elem)
}

// valueReceiversCover reports whether meta's type declares every method in
// required with a value receiver, so the type itself implements the interface.
func valueReceiversCover(meta *transpiler.TypeMetadata, required []string) bool {
	for _, m := range required {
		if mm := meta.Methods[m]; mm == nil || mm.PointerReceiver {
			return false
		}
	}
	return true
}
