package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// The values a call's arguments pass, and the shapes a value filling a slot
// is classified by (classifyExpr). An argument's content (argContent) is the
// expression or lambda it passes, an exprSrc or a lambdaSrc, so lowerAgainst
// and the argument lowering (lowerArg, transformArgument) read the value
// once, the same way.
//
// A postfix expression with a match is lowered by lowerMatch.

// argValue is what an argument passes: an expression, or a lambda written
// directly as the argument (the lambdaExpression alternative of rule
// argument), and whether it spreads a slice, `xs...`.
type argValue struct {
	expr   exprSrc   // the zero exprSrc for a direct lambda
	lambda lambdaSrc // the zero lambdaSrc unless a direct lambda
	spread bool
}

// at is where the value is written.
func (v argValue) at() srcNode {
	if v.lambda.ok() {
		return v.lambda.at()
	}
	return v.expr.at()
}

// argContent is the value a passes. An argument that is neither an
// expression nor a lambda (a typed pattern, as a call-shaped pattern writes
// one) is an error.
func (t *galaASTTransformer) argContent(a argSrc) (argValue, error) {
	t.nativeShape(a.node, "arg")
	if l := (native.ArgumentNode{GalaNodeRef: a.node}).LambdaExpression(); l.Ok() {
		return argValue{lambda: lambdaSrc{node: l}}, nil
	}
	p := a.pattern()
	switch p.kind() {
	case patKindExpr:
		return argValue{expr: nativeExpr(p.expr().node)}, nil
	case patKindRest:
		return argValue{expr: nativeExpr(p.expr().node), spread: true}, nil
	case patKindNone:
		return argValue{}, semanticErrorAtStart(a.at(), "only expressions allowed as function arguments")
	}
	return argValue{}, semanticErrorAtStart(p.at(), "only expressions allowed as function arguments")
}

// solePostfix is the postfix expression e is, with no operator around it,
// and the zero postfixSrc when e is something else.
func (e exprSrc) solePostfix() postfixSrc {
	if !e.ok() {
		return postfixSrc{}
	}
	return patExpr{exprSrc: e}.postfix(true)
}

// hintKey is the key a hint pushed for p's value takes (see hintOwner); no
// owner for the zero postfixSrc.
func (t *galaASTTransformer) hintKey(p postfixSrc) hintOwner {
	return nativeOwner(p.node)
}

// hasTypeArgList reports whether c is written with type arguments and their
// expression list.
func (c callPat) hasTypeArgList() bool {
	if !c.typeArgs.node.Ok() {
		return false
	}
	return indexSrc{c.typeArgs}.hasList()
}

// alone reports whether p is its primary expression only, with no suffix and
// no match.
func (p postfixSrc) alone() bool {
	return p.node.ChildCount() == 1
}

// parenthesized is the first element of e when e is exactly a parenthesized
// expression list — `(e)` or a tuple literal — and the number of its
// elements; 0 when e is no such list.
// Unlike patExpr.parenthesized, it collects no elements: it runs on every
// value lowered against a slot.
func (t *galaASTTransformer) parenthesized(e exprSrc) (first exprSrc, n int) {
	if p := e.solePostfix(); p.ok() && p.alone() {
		return p.parenFirst()
	}
	return exprSrc{}, 0
}

// lambdaIn is the lambda e is, with no suffix applied to it, and the zero
// lambdaSrc otherwise.
func (t *galaASTTransformer) lambdaIn(e exprSrc) lambdaSrc {
	if p := e.solePostfix(); p.ok() && !p.hasSuffixes() {
		return p.primaryLambda()
	}
	return lambdaSrc{}
}

// classifyNative is classifyExpr of e: its sole postfix expression is found
// once, and the form read off its primary's one child or its match.
func (t *galaASTTransformer) classifyNative(e exprSrc) exprForm {
	p := e.solePostfix()
	if !p.ok() {
		return exprForm{}
	}
	alone, suffixed := p.alone(), p.hasSuffixes()
	if prim, ok := firstKidNode(p.primaryExprNode().GalaNodeRef); ok {
		switch prim.Kind() {
		case native.GalaKIND_primary:
			if alone {
				if el, ok := galaparser.OnlyNativeKid(p.tupleListNode(), native.GalaKIND_expression); ok {
					return exprForm{grouped: nativeExpr(el)}
				}
			}
		case native.GalaKIND_lambdaExpression:
			if !suffixed {
				return exprForm{lambda: lambdaSrc{node: native.LambdaExpressionNode{GalaNodeRef: prim}}}
			}
		case native.GalaKIND_partialFunctionLiteral:
			if alone {
				return exprForm{partial: partialSrc{node: prim}}
			}
		case native.GalaKIND_ifExpression:
			if alone {
				return exprForm{ifExpr: ifExprSrc{node: prim}}
			}
		}
	}
	if p.hasCases() {
		return exprForm{match: p}
	}
	return exprForm{}
}

// partialSrc is a partial-function literal, `{ case ... }`.
type partialSrc struct {
	node native.GalaNodeRef
}

// ok reports whether f is a partial function, not the zero partialSrc.
func (f partialSrc) ok() bool { return f.node.Ok() }

// at is where f is written.
func (f partialSrc) at() srcNode { return srcNode{node: f.node} }

// cases is f's case clauses, in order, read as a match's are (nativeCases).
func (f partialSrc) cases(t *galaASTTransformer) []caseSrc {
	return t.nativeCases(f.node)
}

// partialIn is the partial function e's sole operand starts from, whatever
// suffixes follow it, and the zero partialSrc when it starts from anything
// else.
func (t *galaASTTransformer) partialIn(e exprSrc) partialSrc {
	if p := e.solePostfix(); p.ok() {
		return p.primaryPartial()
	}
	return partialSrc{}
}

// ifIn is the if-expression e is, with no suffix or match applied to it
// (`if (c) a else { b } match { ... }` is the match), and the zero ifExprSrc
// otherwise.
func (t *galaASTTransformer) ifIn(e exprSrc) ifExprSrc {
	if p := e.solePostfix(); p.ok() && p.alone() {
		return p.primaryIf()
	}
	return ifExprSrc{}
}

// ok reports whether e is an if-expression, not the zero ifExprSrc.
func (e ifExprSrc) ok() bool { return e.node.Ok() }

// bareMatch is the postfix expression of e when e is a match with no operator
// around it, and the zero postfixSrc otherwise.
func (t *galaASTTransformer) bareMatch(e exprSrc) postfixSrc {
	if p := e.solePostfix(); p.ok() && p.hasCases() {
		return p
	}
	return postfixSrc{}
}

// primaryLambda is the lambda p's primary expression is, and the zero
// lambdaSrc when it is something else.
func (p postfixSrc) primaryLambda() lambdaSrc {
	if l := p.primaryExprNode().LambdaExpression(); l.Ok() {
		return lambdaSrc{node: l}
	}
	return lambdaSrc{}
}

// primaryPartial is the partial function p's primary expression is, and the
// zero partialSrc when it is something else.
func (p postfixSrc) primaryPartial() partialSrc {
	return partialSrc{node: p.primaryExprNode().PartialFunctionLiteral().GalaNodeRef}
}

// primaryIf is the if-expression p's primary expression is, and the zero
// ifExprSrc when it is something else.
func (p postfixSrc) primaryIf() ifExprSrc {
	return ifExprSrc{node: p.primaryExprNode().IfExpression().GalaNodeRef}
}

// parenFirst is the first element of the parenthesized expression list p's
// primary is, and the number of its elements; 0 when its primary is no such
// list.
func (p postfixSrc) parenFirst() (first exprSrc, n int) {
	for el := range galaparser.NativeKids(p.tupleListNode(), native.GalaKIND_expression) {
		if n == 0 {
			first = nativeExpr(el)
		}
		n++
	}
	return first, n
}

// countPlaceholders is the number of `_` placeholders written in e outside any
// lambda and case clause.
func countPlaceholders(e exprSrc) int {
	if !e.node.Ok() {
		return 0
	}
	return countPlaceholdersNative(e.node)
}

// countPlaceholdersNative is countPlaceholders of the node n.
func countPlaceholdersNative(n native.GalaNodeRef) int {
	switch n.Kind() {
	case native.GalaKIND_lambdaExpression, native.GalaKIND_caseClause:
		// A nested lambda's `_` belong to its own body, and a case clause's
		// are wildcards.
		return 0
	}
	total := 0
	for i := range n.ChildCount() {
		c := n.Child(i)
		switch {
		case !c.IsToken():
			total += countPlaceholdersNative(c.Node())
		case c.Terminal().GetText() == "_":
			total++
		}
	}
	return total
}

// escapesNative is escapes of the native node n.
func escapesNative(n native.GalaNodeRef, inLoop bool) bool {
	switch n.Kind() {
	case native.GalaKIND_lambdaExpression:
		return false
	case native.GalaKIND_returnStatement:
		return true
	case native.GalaKIND_forStatement:
		inLoop = true
	}
	for i := range n.ChildCount() {
		c := n.Child(i)
		if c.IsToken() {
			if _, ok := loopControlToken(c.Terminal().GetText()); ok && !inLoop {
				return true
			}
			continue
		}
		if escapesNative(c.Node(), inLoop) {
			return true
		}
	}
	return false
}

// escapesAt reports whether the subtree at n escapes the construct it is
// lowered in: a `return`, or a `break`/`continue` outside a loop of its own,
// not inside a lambda. The answer is cached by node.
func (t *galaASTTransformer) escapesAt(n srcNode) bool {
	return t.cachedEscape(n.node, func() bool { return escapesNative(n.node, false) })
}

// matchEscapes reports whether a case clause of p, a match, escapes (see
// escapesAt). It is a scan ahead of the lowering, so it reads the clauses
// without reporting them to the onNativeCase hook (nativeCases) and stops at
// the first that escapes.
func (t *galaASTTransformer) matchEscapes(p postfixSrc) bool {
	for c := range galaparser.NativeKids(p.node, native.GalaKIND_caseClause) {
		if t.escapesAt(srcNode{node: c}) {
			return true
		}
	}
	return false
}
