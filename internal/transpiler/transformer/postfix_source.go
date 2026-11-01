package transformer

import (
	"go/ast"
	"iter"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// The postfix chain on galop's native tree: a primary expression followed by
// its suffixes, each a member access `.name`, an index or type arguments
// `[exprs]`, or a call `(args)`. Both walks lower the chain through one
// driver (lowerSuffixes), reading each suffix on the tree it comes from
// through suffixSrc (memberSrc, indexSrc, and callSrc in calls_source.go),
// so both give the same Go. A postfix expression with a match is still
// lowered on ANTLR's tree as a whole (crossTo): the match is the match's.
//
// The native readers dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// memberSrc is a member access suffix `.name`.
type memberSrc struct{ suffixSrc }

// indexSrc is an index or type-argument suffix `[exprs]`.
type indexSrc struct{ suffixSrc }

// at is where s is written.
func (s suffixSrc) at() srcNode {
	if s.node.Ok() {
		return srcNode{node: s.node}
	}
	return srcNode{ctx: s.ctx}
}

// identAt is where the member s selects is written, the zero srcNode when s
// is no selector.
func (s suffixSrc) identAt() srcNode {
	_, at, _ := s.member()
	return at
}

// antlr is s's context, and false when s is a native node without one.
func (s suffixSrc) antlr(t *galaASTTransformer) (*grammar.PostfixSuffixContext, bool) {
	if !s.node.Ok() {
		return s.ctx, s.ctx != nil
	}
	ctx, ok := t.native.Ctx(s.node).(*grammar.PostfixSuffixContext)
	return ctx, ok
}

// hasList reports whether ix has its expression list, which only a malformed
// tree lacks.
func (ix indexSrc) hasList() bool {
	if ix.node.Ok() {
		return (native.PostfixSuffixNode{GalaNodeRef: ix.node}).ExpressionList().Ok()
	}
	return ix.ctx.ExpressionList() != nil
}

// exprs is the expressions of ix, in order, read off the tree as they are
// iterated.
func (ix indexSrc) exprs() iter.Seq[exprSrc] {
	return func(yield func(exprSrc) bool) {
		if ix.node.Ok() {
			list := (native.PostfixSuffixNode{GalaNodeRef: ix.node}).ExpressionList()
			for e := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_expression) {
				if !yield(nativeExpr(e)) {
					return
				}
			}
			return
		}
		list := ix.ctx.ExpressionList()
		if list == nil {
			return
		}
		for _, c := range list.GetChildren() {
			if e, ok := c.(grammar.IExpressionContext); ok && !yield(antlrExpr(e)) {
				return
			}
		}
	}
}

// count is the number of ix's expressions.
func (ix indexSrc) count() int {
	n := 0
	for range ix.exprs() {
		n++
	}
	return n
}

// expr is the i-th expression of ix, the zero exprSrc when it has fewer.
func (ix indexSrc) expr(i int) exprSrc {
	for e := range ix.exprs() {
		if i == 0 {
			return e
		}
		i--
	}
	return exprSrc{}
}

// lowerSuffixedNative is transformPostfixExpr of p when p has a suffix or a
// match. Its suffixes are lowered through the postfix driver (lowerSuffixes);
// a match hands p to ANTLR's tree whole.
func (t *galaASTTransformer) lowerSuffixedNative(p native.PostfixExprNode) (ast.Expr, error) {
	// The hint is keyed by p's context, as the ANTLR walk pushes it.
	owner := postfixSrc{node: p.GalaNodeRef}.hintKey(t)
	src := postfixSrc{node: p.GalaNodeRef, owner: owner}
	// Most chains are short: no allocation for them. A child that is no
	// suffix is part of a match.
	var buf [4]suffixSrc
	suffixes := src.appendSuffixes(buf[:0])
	if len(suffixes) != p.ChildCount()-1 {
		return t.transformPostfixExpr(crossTo[*grammar.PostfixExprContext](t, p.GalaNodeRef))
	}
	return t.lowerSuffixes(src, suffixes)
}

// lowerSuffixes lowers p, a postfix expression with suffixes and no match,
// on the tree it comes from: its primary expression, then each of suffixes
// applied to the value before it.
func (t *galaASTTransformer) lowerSuffixes(p postfixSrc, suffixes []suffixSrc) (ast.Expr, error) {
	// The type of the slot p fills is for its last suffix, the call that is
	// the value (see consumesSlotType): never for the receiver the call is
	// applied to.
	release := t.expectedArgTypes.withhold(p.owner)
	defer release()
	var result ast.Expr
	var err error
	if p.node.Ok() {
		result, err = t.lowerPrimaryExprNative(p.primaryExprNode().GalaNodeRef)
	} else {
		result, err = t.transformPrimaryExpr(p.ctx.PrimaryExpr().(*grammar.PrimaryExprContext))
	}
	if err != nil {
		return nil, err
	}
	for i, s := range suffixes {
		if i == len(suffixes)-1 {
			release()
		}
		if result, err = t.lowerSuffix(result, p, s); err != nil {
			return nil, err
		}
	}
	// A call that produces no value may only stand alone (see void_calls.go).
	if last := suffixes[len(suffixes)-1]; last.opener() == "(" {
		t.recordVoidCall(callSrc{post: p, suffix: last}, result)
	}
	return result, nil
}

// lowerSuffix applies s, a suffix of p, to base: a member access, an index
// or type arguments, or a call.
func (t *galaASTTransformer) lowerSuffix(base ast.Expr, p postfixSrc, s suffixSrc) (ast.Expr, error) {
	if s.ident() != "" {
		t.suffixShape(s, "selector")
		return t.lowerMember(base, memberSrc{s})
	}
	if s.childCount() >= 2 {
		switch s.opener() {
		case "(":
			t.suffixShape(s, "call")
			return t.applyGoCallSuffix(base, callSrc{post: p, suffix: s, args: s.args()})
		case "[":
			t.suffixShape(s, "index")
			return t.resolveIndexAccess(base, indexSrc{s})
		}
	}
	return nil, semanticErrorAtStart(s.at(), "unknown postfix suffix type")
}

// suffixShape is nativeShape of s when it is read on the native tree.
func (t *galaASTTransformer) suffixShape(s suffixSrc, shape string) {
	if s.node.Ok() {
		t.nativeShape(s.node, shape)
	}
}
