package transformer

import (
	"go/ast"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// The postfix chain on galop's native tree: a primary expression followed by
// its suffixes, each a member access `.name`, an index or type arguments
// `[exprs]`, or a call `(args)`. A member access and an index are read here
// on either tree (memberSrc, indexSrc) and lowered once, by lowerMember and
// resolveIndexAccess, so both walks give the same Go. A postfix expression
// with a call suffix or a match is still lowered on ANTLR's tree as a whole
// (crossTo): the call lowering reads the argument list and the expression it
// ends, and the match is the match's.
//
// The native readers dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// memberSrc is a member access suffix `.name`: the name, where it is written,
// and the suffix, where a diagnostic about the access points.
type memberSrc struct {
	name   string
	nameAt srcNode
	suffix srcNode
}

// indexSrc is an index or type-argument suffix `[exprs]`: its expressions and
// where it is written. hasList is false for a suffix without its list, which
// only a malformed tree has.
type indexSrc struct {
	exprs   []exprSrc
	hasList bool
	at      srcNode
}

// antlrIndex reads the index suffix ctx on ANTLR's tree.
func antlrIndex(ctx *grammar.PostfixSuffixContext) indexSrc {
	ix := indexSrc{at: srcNode{ctx: ctx}}
	list := ctx.ExpressionList()
	if list == nil {
		return ix
	}
	ix.hasList = true
	for _, e := range list.AllExpression() {
		ix.exprs = append(ix.exprs, antlrExpr(e))
	}
	return ix
}

// nativeIndex is antlrIndex of s.
func nativeIndex(s native.PostfixSuffixNode) indexSrc {
	ix := indexSrc{at: srcNode{node: s.GalaNodeRef}}
	list := s.ExpressionList()
	if !list.Ok() {
		return ix
	}
	ix.hasList = true
	for e := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_expression) {
		ix.exprs = append(ix.exprs, nativeExpr(e))
	}
	return ix
}

// suffixOpenerNative is the token the native suffix s starts with: ".", "(" or
// "[", and "" when it starts with no token.
func suffixOpenerNative(s native.PostfixSuffixNode) string {
	if s.ChildCount() == 0 {
		return ""
	}
	if c := s.Child(0); c.IsToken() {
		return c.Terminal().GetText()
	}
	return ""
}

// lowerSuffixedNative is transformPostfixExpr of p, whose primary expression
// is prim, when p has a suffix or a match. Its member accesses and indexes
// are lowered here; a call suffix or a match hands p to ANTLR's tree whole.
func (t *galaASTTransformer) lowerSuffixedNative(p native.PostfixExprNode, prim native.GalaNodeRef) (ast.Expr, error) {
	// Most chains are short: no allocation for them.
	var buf [4]native.PostfixSuffixNode
	suffixes := buf[:0]
	for i := 1; i < p.ChildCount(); i++ {
		n := p.Child(i).Node()
		if !n.Ok() || n.Kind() != native.GalaKIND_postfixSuffix {
			// The `match` keyword, its braces and its case clauses.
			return t.transformPostfixExpr(crossTo[*grammar.PostfixExprContext](t, p.GalaNodeRef))
		}
		s := native.PostfixSuffixNode{GalaNodeRef: n}
		if suffixOpenerNative(s) == "(" {
			return t.transformPostfixExpr(crossTo[*grammar.PostfixExprContext](t, p.GalaNodeRef))
		}
		suffixes = append(suffixes, s)
	}
	// The type of the slot p fills is for its last suffix, never for the
	// receiver it is applied to (see transformPostfixExpr).
	release := t.expectedArgTypes.withhold(postfixOwner{node: p.GalaNodeRef})
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
func (t *galaASTTransformer) lowerSuffixNative(base ast.Expr, s native.PostfixSuffixNode) (ast.Expr, error) {
	if id := s.Identifier(); id.Ok() {
		t.nativeShape(s.GalaNodeRef, "selector")
		return t.lowerMember(base, memberSrc{name: galaparser.NativeText(id.GalaNodeRef), nameAt: srcNode{node: id.GalaNodeRef}, suffix: srcNode{node: s.GalaNodeRef}})
	}
	if s.ChildCount() >= 2 && suffixOpenerNative(s) == "[" {
		t.nativeShape(s.GalaNodeRef, "index")
		return t.resolveIndexAccess(base, nativeIndex(s))
	}
	return nil, semanticErrorAtStart(srcNode{node: s.GalaNodeRef}, "unknown postfix suffix type")
}
