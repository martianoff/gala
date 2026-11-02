package transformer

import (
	"fmt"
	"go/ast"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// Taking the address of an immutable value
//
// `&x` yields a *T, and a *T writes. A val, a parameter not declared `var`, a
// receiver, an immutable field and a field reached through a val cannot be
// changed, so none of them has an address (GALA-E0076): the value is passed
// itself, or the binding is declared `var` where Go needs a *T.

// immutableAddressError reports `&operand` when operand is immutable, or nil.
// at is the unary expression, operandAt its operand (a unaryExpr), and
// operand the operand's lowering, used for the field paths a bare name does
// not cover. Only a diagnostic reads the operand's source text.
func (t *galaASTTransformer) immutableAddressError(at, operandAt srcNode, operand ast.Expr) error {
	name, fixed := t.addressOperandBinding(operandAt)
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
	text := operandAt.sourceText()
	line, col := at.start()
	return galaerr.NewCodedSemanticError(galaerr.CodeAddressOfImmutable, line, col,
		fmt.Sprintf("cannot take the address of %s: %s", text, why), hint,
	).WithSpan(col + 1 + len([]rune(text)))
}

// addressOperandBinding is immutablePostfixBinding of operand, a unaryExpr,
// when it is a postfix expression with no unary operator.
func (t *galaASTTransformer) addressOperandBinding(operand srcNode) (string, fixedBinding) {
	var p postfixSrc
	if u := (native.UnaryExprNode{GalaNodeRef: operand.node}); operand.node.Ok() && !u.UnaryOp().Ok() {
		p = postfixSrc{node: u.PostfixExpr().GalaNodeRef}
	}
	return t.immutablePostfixBinding(p)
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
func (t *galaASTTransformer) immutableFieldWriteError(targetAt srcNode, target ast.Expr, verb string) error {
	if _, bare := target.(*ast.Ident); bare {
		return nil
	}
	why, hint := t.immutablePathReason(target)
	if why == "" {
		return nil
	}
	err := t.semanticErrorAtNode(targetAt, fmt.Sprintf("cannot %s %s: %s", verb, targetAt.sourceText(), why))
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
	case *ast.Ident:
		// A local val is a plain Go variable, so Go would let the write
		// through; a package-level one is read through `.Get()` (below).
		if t.isPlainVal(e.Name) {
			return reachedThroughVal(e.Name)
		}
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
			return reachedThroughVal(x.Name)
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

// reachedThroughVal is immutablePathReason's answer for a path rooted at the
// val name.
func reachedThroughVal(name string) (why, hint string) {
	return fmt.Sprintf("it is reached through val %s", name), fmt.Sprintf("declare it `var %s`", name)
}

// A local val is a plain Go variable, so Go itself would let a write, an `&`
// or a pointer-receiver call through any path rooted at it. GALA rejects the
// ones it can judge (immutablePathReason, addressableReceiver); a path it
// cannot judge — one that indexes (a Go array and a slice look alike to it)
// or crosses a value of unresolved type — keeps the val's boxed shape at its
// root: `std.NewImmutable(x).Get()`, a call result, which Go addresses
// exactly as it did the boxed val's `x.Get()`. Go then rejects a write to an
// array element or a struct field, and lets one through a slice, map or
// pointer.

// sealPlainValPath returns path with its root re-boxed when the path is
// rooted at a local val, crosses no known pointer, and indexes or crosses a
// value of unresolved type; otherwise path itself.
func (t *galaASTTransformer) sealPlainValPath(path ast.Expr) ast.Expr {
	root, judged := t.plainValPathRoot(path)
	if root == nil || judged {
		return path
	}
	boxed := &ast.CallExpr{Fun: &ast.SelectorExpr{
		X:   &ast.CallExpr{Fun: t.stdIdent(transpiler.FuncNewImmutable), Args: []ast.Expr{root}},
		Sel: ast.NewIdent(transpiler.MethodGet),
	}}
	return replacePathRoot(path, boxed)
}

// plainValPathRoot returns the local val a selector/index path is rooted at,
// or nil when the path crosses a known pointer or has another root. judged
// reports that every value on the path has a resolved type and none is
// indexed, so GALA's own checks already decided it.
func (t *galaASTTransformer) plainValPathRoot(path ast.Expr) (root *ast.Ident, judged bool) {
	// Find the root first, without typing anything: most paths are not
	// rooted at a local val.
	if id, ok := pathRootIdent(path); !ok || !t.isPlainVal(id.Name) {
		return nil, false
	}
	judged = true
	for {
		switch e := path.(type) {
		case *ast.Ident:
			return e, judged && !transpiler.IsUnusable(t.getExprTypeName(e))
		case *ast.ParenExpr:
			path = e.X
		case *ast.SelectorExpr:
			xType := t.getExprTypeName(e.X)
			if _, isPtr := xType.(transpiler.PointerType); isPtr {
				return nil, false
			}
			judged = judged && !transpiler.IsUnusable(xType)
			path = e.X
		case *ast.IndexExpr:
			switch t.getExprTypeName(e.X).(type) {
			case transpiler.PointerType:
				return nil, false
			case transpiler.MapType:
				// A map element is written in the shared map, whichever
				// way the map is reached, and is never addressable.
				return nil, false
			}
			judged = false
			path = e.X
		default:
			return nil, false
		}
	}
}

// replacePathRoot rebuilds a selector/index path with its root ident
// replaced by root.
func replacePathRoot(path, root ast.Expr) ast.Expr {
	switch e := path.(type) {
	case *ast.ParenExpr:
		return &ast.ParenExpr{X: replacePathRoot(e.X, root)}
	case *ast.SelectorExpr:
		return &ast.SelectorExpr{X: replacePathRoot(e.X, root), Sel: e.Sel}
	case *ast.IndexExpr:
		return &ast.IndexExpr{X: replacePathRoot(e.X, root), Index: e.Index}
	}
	return root
}

// pathRootIdent returns the ident a selector/index path starts from.
func pathRootIdent(path ast.Expr) (*ast.Ident, bool) {
	for {
		switch e := path.(type) {
		case *ast.Ident:
			return e, true
		case *ast.ParenExpr:
			path = e.X
		case *ast.SelectorExpr:
			path = e.X
		case *ast.IndexExpr:
			path = e.X
		default:
			return nil, false
		}
	}
}
