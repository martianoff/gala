package transformer

import (
	"go/ast"
	"iter"
	"strings"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The postfix chain on galop's native tree: a primary expression followed by
// its suffixes, each a member access `.name`, an index or type arguments
// `[exprs]`, or a call `(args)`. The chain is lowered through one driver
// (lowerSuffixes), reading each suffix through suffixSrc (memberSrc,
// indexSrc, and callSrc in calls_source.go). A postfix expression with a
// match is lowered by lowerMatch, its subject through the same suffix
// lowering.
//
// The native readers dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// memberSrc is a member access suffix `.name`; called says the suffix after
// it is a call, which makes the member a method called.
type memberSrc struct {
	suffixSrc
	called bool
}

// indexSrc is an index or type-argument suffix `[exprs]`.
type indexSrc struct{ suffixSrc }

// at is where s is written.
func (s suffixSrc) at() srcNode { return srcNode{node: s.node} }

// identAt is where the member s selects is written, the zero srcNode when s
// is no selector.
func (s suffixSrc) identAt() srcNode {
	_, at, _ := s.member()
	return at
}

// receiver is the receiver of the member access m: the postfix expression's
// primary through the suffix before m, as the nodes spanning it and its
// source text, each part as written. The grammar puts a suffix only in a
// postfix expression.
func (m memberSrc) receiver(t *galaASTTransformer) (from, to srcNode, text string) {
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
	return (native.PostfixSuffixNode{GalaNodeRef: ix.node}).ExpressionList().Ok()
}

// exprs is the expressions of ix, in order, read off the tree as they are
// iterated.
func (ix indexSrc) exprs() iter.Seq[exprSrc] {
	return func(yield func(exprSrc) bool) {
		list := (native.PostfixSuffixNode{GalaNodeRef: ix.node}).ExpressionList()
		for e := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_expression) {
			if !yield(nativeExpr(e)) {
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

// lowerSuffixedNative lowers p, a postfix expression with a suffix or a
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

// lowerSuffixes lowers p, a postfix expression with suffixes and no match:
// its primary expression, then each of suffixes
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
		if i == len(suffixes)-1 {
			release()
		}
		if result, err = t.lowerSuffix(result, p, suffixes, i); err != nil {
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
// first suffix, call, when that is a call with one positional argument
// (`{ case n => n * 2 }(3)`): the argument's type, when it is known and
// concrete, is the literal's parameter type, as a function slot's would be,
// and its patterns are matched against it. It reports false, lowering
// nothing, for any other p.
func (t *galaASTTransformer) lowerAppliedPartial(p postfixSrc, call suffixSrc) (ast.Expr, bool, error) {
	pf := p.primaryPartial()
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
	arg, err := t.lowerExpr(v.expr)
	if err != nil {
		return nil, false, err
	}
	var s slot
	if argType := t.getExprTypeName(arg); !transpiler.IsUnusableOrAny(argType) &&
		!transpiler.ContainsUnusable(argType) && !t.mentionsUnboundTypeParam(argType) {
		s.typ = transpiler.FuncType{Params: []transpiler.Type{argType}}
	}
	lit, err := t.transformPartialFunctionLiteral(pf, s)
	if err != nil {
		return nil, false, err
	}
	return &ast.CallExpr{Fun: lit, Args: []ast.Expr{arg}}, true, nil
}

// lowerSuffix applies suffixes[i], a suffix of p, to base: a member access, an index
// or type arguments, or a call.
func (t *galaASTTransformer) lowerSuffix(base ast.Expr, p postfixSrc, suffixes []suffixSrc, i int) (ast.Expr, error) {
	s := suffixes[i]
	if s.ident() != "" {
		t.suffixShape(s, "selector")
		called := i+1 < len(suffixes) && suffixes[i+1].opener() == "("
		return t.lowerMember(base, memberSrc{s, called})
	}
	if s.childCount() >= 2 {
		switch s.opener() {
		case "(":
			t.suffixShape(s, "call")
			args := s.args()
			copyCall := i > 0 && suffixes[i-1].ident() == "Copy"
			if err := checkPositionalAfterNamed(args, copyCall); err != nil {
				return nil, err
			}
			return t.applyGoCallSuffix(base, callSrc{post: p, suffix: s, args: args})
		case "[":
			t.suffixShape(s, "index")
			return t.resolveIndexAccess(base, indexSrc{s})
		}
	}
	return nil, semanticErrorAtStart(s.at(), "unknown postfix suffix type")
}

// suffixShape is nativeShape of s.
func (t *galaASTTransformer) suffixShape(s suffixSrc, shape string) {
	if s.node.Ok() {
		t.nativeShape(s.node, shape)
	}
}
