package transformer

import (
	"go/ast"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The parts of a declaration as their lowerings read them, from either tree:
// an expression list (a val's or var's initializers, an assignment's sides)
// and a signature (its parameters, read as paramDecls, and its results).
// Each reader has an ANTLR half and a native half that give the same value,
// so a declaration is lowered once, the same way, whichever tree it comes
// from. The native halves read children through the rule handles and
// NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// ok reports whether l is a list, not the zero exprListSrc.
func (l exprListSrc) ok() bool { return l.node.Ok() || l.ctx != nil }

// at is where l is written.
func (l exprListSrc) at() srcNode {
	if l.node.Ok() {
		return srcNode{node: l.node}
	}
	return srcNode{ctx: l.ctx}
}

// noteNativeShape tells onNativeShape that the native walk read n, a part of
// a declaration of shape, when n is a node of the native tree.
func (t *galaASTTransformer) noteNativeShape(n srcNode, shape string) {
	if n.node.Ok() {
		t.nativeShape(n.node, shape)
	}
}

// sigSrc is a signature, of a function, a method or an interface's method
// spec: a context on ANTLR's tree, or a node on the native one.
type sigSrc struct {
	ctx  *grammar.SignatureContext
	node native.SignatureNode
}

// antlrSig is the signature ctx, on ANTLR's tree.
func antlrSig(ctx grammar.ISignatureContext) sigSrc {
	return sigSrc{ctx: ctx.(*grammar.SignatureContext)}
}

// nativeSig is the signature node n, which its reader requires: a missing one
// is a bug in the walk or the bridge, raised as an internal error (see
// nativeExpr).
func nativeSig(n native.SignatureNode) sigSrc {
	if !n.Ok() {
		// invariant: native-bridge — the walk reads only bridged trees.
		panic("native tree: no node, not a signature")
	}
	return sigSrc{node: n}
}

// params is s's parameters, in order.
func (s sigSrc) params() []paramDecl {
	if s.node.Ok() {
		return readParamsNative(s.node.Parameters())
	}
	return readParams(s.ctx.Parameters().(*grammar.ParametersContext))
}

// results is s's result types: its one result type, or each type of its Go
// result list (`(int, error)`).
func (s sigSrc) results() []typeSrc {
	if s.node.Ok() {
		return nativeSigResults(s.node)
	}
	return antlrSigResults(s.ctx)
}

// resultType is s's one result type, no type when it has none or a Go result
// list.
func (s sigSrc) resultType() typeSrc {
	if s.node.Ok() {
		return nativeType(s.node.Type_())
	}
	return antlrType(s.ctx.Type_())
}

// goResults is where s's Go result list is written, and false when it has
// none.
func (s sigSrc) goResults() (srcNode, bool) {
	if s.node.Ok() {
		gr := s.node.GoResults()
		return srcNode{node: gr.GalaNodeRef}, gr.Ok()
	}
	gr, ok := s.ctx.GoResults().(*grammar.GoResultsContext)
	if !ok || gr == nil {
		return srcNode{}, false
	}
	return srcNode{ctx: gr}, true
}

// hasGoResults reports whether s declares a Go result list.
func (s sigSrc) hasGoResults() bool {
	_, ok := s.goResults()
	return ok
}

// exprsOf is l's expressions, each on the native tree when the file's walk
// reads that tree (exprOf): a list reached from code still on ANTLR's tree is
// lowered on its native nodes all the same.
func (t *galaASTTransformer) exprsOf(l exprListSrc) []exprSrc {
	if l.node.Ok() {
		t.nativeShape(l.node, "init")
		return l.exprs()
	}
	if l.ctx == nil {
		return nil
	}
	all := l.ctx.AllExpression()
	out := make([]exprSrc, len(all))
	for i, e := range all {
		out[i] = t.exprOf(e)
	}
	return out
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
