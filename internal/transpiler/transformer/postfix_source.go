package transformer

import (
	"go/ast"
	"iter"
	"strings"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The postfix chain on galop's native tree: a primary expression followed by
// its suffixes, each a member access `.name`, an index or type arguments
// `[exprs]`, or a call `(args)`. Both walks lower the chain through one
// driver (lowerSuffixes), reading each suffix on the tree it comes from
// through suffixSrc (memberSrc, indexSrc, and callSrc in calls_source.go),
// so both give the same Go. A postfix expression with a match is lowered by
// lowerMatch, its subject through the same suffix lowering.
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

// receiver is receiverSource of m: the receiver of the member access, the
// postfix expression's primary through the suffix before m, as the nodes
// spanning it and its source text. The grammar puts a suffix only in a
// postfix expression.
func (m memberSrc) receiver(t *galaASTTransformer) (from, to srcNode, text string) {
	if !m.node.Ok() {
		f, l, text := receiverSource(m.ctx)
		return srcNode{ctx: f}, srcNode{ctx: l}, text
	}
	t.nativeShape(m.node, "receiver")
	postfix := m.node.Parent()
	primary := (native.PostfixExprNode{GalaNodeRef: postfix}).PrimaryExpr().GalaNodeRef
	from, to = srcNode{node: primary}, srcNode{node: primary}
	parts := []string{primary.SourceText()}
	for s := range galaparser.NativeKids(postfix, native.GalaKIND_postfixSuffix) {
		if s == m.node {
			break
		}
		to = srcNode{node: s}
		parts = append(parts, s.SourceText())
	}
	return from, to, strings.Join(parts, "")
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
// match. Its suffixes are lowered through the postfix driver (lowerSuffixes),
// and a match by lowerMatch.
func (t *galaASTTransformer) lowerSuffixedNative(p native.PostfixExprNode) (ast.Expr, error) {
	// The hint is keyed by p's node, as it is pushed (hintOwner).
	src := postfixSrc{node: p.GalaNodeRef, owner: nativeOwner(p.GalaNodeRef)}
	// Most chains are short: no allocation for them. A child that is no
	// suffix is part of a match.
	var buf [4]suffixSrc
	suffixes := src.appendSuffixes(buf[:0])
	if len(suffixes) != p.ChildCount()-1 {
		return t.lowerMatch(src, slot{})
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
	result, applied, err := t.lowerAppliedPartial(p, suffixes[0])
	if err != nil {
		return nil, err
	}
	first := 0
	if applied {
		first = 1
	} else if result, err = t.lowerPrimaryOf(p); err != nil {
		return nil, err
	}
	for i := first; i < len(suffixes); i++ {
		s := suffixes[i]
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

// lowerAppliedPartial lowers p's partial-function primary applied to its
// first suffix, call, when that is a call with one positional argument and
// the patterns name no parameter type (`{ case n => n * 2 }(3)`): the
// argument's type, when known, is the literal's parameter type, as a slot's
// would be. It reports false, lowering nothing, for any other p.
func (t *galaASTTransformer) lowerAppliedPartial(p postfixSrc, call suffixSrc) (ast.Expr, bool, error) {
	pf := p.primaryPartial(t)
	if !pf.ok() || call.ident() != "" || call.childCount() < 2 || call.opener() != "(" {
		return nil, false, nil
	}
	args := call.args().list
	if len(args) != 1 || args[0].named() {
		return nil, false, nil
	}
	v, err := t.argContent(args[0])
	if err != nil || !v.expr.ok() || v.spread {
		return nil, false, nil
	}
	if !transpiler.IsUnusable(t.inferPartialFunctionParamType(pf.cases(t))) {
		return nil, false, nil
	}
	arg, err := t.lowerExpr(v.expr)
	if err != nil {
		return nil, false, err
	}
	var s slot
	if argType := t.getExprTypeName(arg); !transpiler.IsUnusableOrAny(argType) {
		s.typ = transpiler.FuncType{Params: []transpiler.Type{argType}}
	}
	lit, err := t.transformPartialFunctionLiteral(pf, s)
	if err != nil {
		return nil, false, err
	}
	return &ast.CallExpr{Fun: lit, Args: []ast.Expr{arg}}, true, nil
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
