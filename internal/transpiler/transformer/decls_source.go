package transformer

import (
	"go/ast"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The parts of a declaration as their lowerings read them: an expression
// list (a val's or var's initializers, an assignment's sides) and a
// signature (its parameters, read as paramDecls, and its results). The
// readers read children through the rule handles and NativeKids, never
// through Typed() or the All accessors (see declarations_native.go).

// ok reports whether l is a list, not the zero exprListSrc.
func (l exprListSrc) ok() bool { return l.node.Ok() }

// at is where l is written.
func (l exprListSrc) at() srcNode { return srcNode{node: l.node} }

// noteNativeShape tells onNativeShape that the native walk read n, a part of
// a declaration of shape, when n is a node.
func (t *galaASTTransformer) noteNativeShape(n srcNode, shape string) {
	if n.node.Ok() {
		t.nativeShape(n.node, shape)
	}
}

// sigSrc is a signature, of a function, a method or an interface's method
// spec.
type sigSrc struct {
	node native.SignatureNode
}

// nativeSig is the signature node n, which its reader requires: a missing one
// is a bug in the walk, raised as an internal error (see nativeExpr).
func nativeSig(n native.SignatureNode) sigSrc {
	if !n.Ok() {
		// invariant: native-tree — a clean parse has every required node.
		panic("native tree: no node, not a signature")
	}
	return sigSrc{node: n}
}

// params is s's parameters, in order.
func (s sigSrc) params() []paramDecl {
	return readParamsNative(s.node.Parameters())
}

// results is s's result types: its one result type, or each type of its Go
// result list (`(int, error)`).
func (s sigSrc) results() []typeSrc {
	return nativeSigResults(s.node)
}

// resultType is s's one result type, no type when it has none or a Go result
// list.
func (s sigSrc) resultType() typeSrc {
	return nativeType(s.node.Type_())
}

// goResults is where s's Go result list is written, and false when it has
// none.
func (s sigSrc) goResults() (srcNode, bool) {
	gr := s.node.GoResults()
	return srcNode{node: gr.GalaNodeRef}, gr.Ok()
}

// hasGoResults reports whether s declares a Go result list.
func (s sigSrc) hasGoResults() bool {
	_, ok := s.goResults()
	return ok
}

// exprsOf is l's expressions, none for the zero list.
func (t *galaASTTransformer) exprsOf(l exprListSrc) []exprSrc {
	if !l.node.Ok() {
		return nil
	}
	t.nativeShape(l.node, "init")
	return l.exprs()
}

// lowerExprsAgainst lowers exprs, a declaration's or assignment's right-hand
// side: a single expression is lowered against typedSlot(expected).
func (t *galaASTTransformer) lowerExprsAgainst(exprs []exprSrc, expected transpiler.Type) ([]ast.Expr, error) {
	if len(exprs) != 1 {
		return t.lowerExprs(exprs)
	}
	e, err := t.lowerAgainst(exprs[0], typedSlot(expected), true)
	if err != nil {
		return nil, err
	}
	return []ast.Expr{e}, nil
}
