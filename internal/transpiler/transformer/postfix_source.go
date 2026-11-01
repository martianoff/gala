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
// `[exprs]`, or a call `(args)`. A member access and an index are read on
// either tree through suffixSrc (memberSrc, indexSrc) and lowered once, by
// lowerMember and resolveIndexAccess, so both walks give the same Go. A
// postfix expression with a call suffix or a match is still lowered on
// ANTLR's tree as a whole (crossTo): the call lowering reads the argument
// list and the expression it ends, and the match is the match's.
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
	if s.node.Ok() {
		if id := (native.PostfixSuffixNode{GalaNodeRef: s.node}).Identifier(); id.Ok() {
			return srcNode{node: id.GalaNodeRef}
		}
		return srcNode{}
	}
	if id := s.ctx.Identifier(); id != nil {
		return srcNode{ctx: id}
	}
	return srcNode{}
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

// lowerSuffixedNative is transformPostfixExpr of p, whose primary expression
// is prim, when p has a suffix or a match. Its member accesses and indexes
// are lowered here; a call suffix or a match hands p to ANTLR's tree whole.
func (t *galaASTTransformer) lowerSuffixedNative(p native.PostfixExprNode, prim native.GalaNodeRef) (ast.Expr, error) {
	src := postfixSrc{node: p.GalaNodeRef}
	// Most chains are short: no allocation for them. A child that is no
	// suffix is part of a match.
	var buf [4]suffixSrc
	suffixes := src.appendSuffixes(buf[:0])
	if len(suffixes) != p.ChildCount()-1 {
		return t.transformPostfixExpr(crossTo[*grammar.PostfixExprContext](t, p.GalaNodeRef))
	}
	for _, s := range suffixes {
		if s.opener() == "(" {
			return t.transformPostfixExpr(crossTo[*grammar.PostfixExprContext](t, p.GalaNodeRef))
		}
	}
	// The type of the slot p fills is for its last suffix, never for the
	// receiver it is applied to (see transformPostfixExpr). The hint is keyed
	// by p's context, as the ANTLR walk pushes it.
	owner, _ := t.native.Ctx(p.GalaNodeRef).(*grammar.PostfixExprContext)
	release := t.expectedArgTypes.withhold(owner)
	defer release()
	result, err := t.lowerPrimaryExprNative(prim)
	if err != nil {
		return nil, err
	}
	for i, s := range suffixes {
		if i == len(suffixes)-1 {
			release()
		}
		if result, err = t.lowerSuffixNative(result, s); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// lowerSuffixNative is applyPostfixSuffix of s, a member access or an index.
func (t *galaASTTransformer) lowerSuffixNative(base ast.Expr, s suffixSrc) (ast.Expr, error) {
	if s.ident() != "" {
		t.nativeShape(s.node, "selector")
		return t.lowerMember(base, memberSrc{s})
	}
	if s.childCount() >= 2 && s.opener() == "[" {
		t.nativeShape(s.node, "index")
		return t.resolveIndexAccess(base, indexSrc{s})
	}
	return nil, semanticErrorAtStart(s.at(), "unknown postfix suffix type")
}
