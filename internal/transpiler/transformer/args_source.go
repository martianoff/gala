package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// The values a call's arguments pass, and the shapes a value filling a slot
// is classified by (classifyExpr), on either tree. An argument's content
// (argContent) is the expression or lambda it passes, an exprSrc or a
// lambdaSrc on the tree its call is read on; the readers below have an ANTLR
// half and a native half that give the same answer for the same expression,
// so lowerAgainst and the argument lowering (lowerArg, transformArgument)
// read the value once, the same way, on either tree.
//
// What they still hand to ANTLR's tree, with crossTo: a partial function
// (transformPartialFunctionLiteral) and a postfix expression with a match
// (transformPostfixMatchExpressionAgainst), whose lowerings read ANTLR's tree.

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

// argContent is the value a passes (extractArgContent), on a's tree. An
// argument that is neither an expression nor a lambda (a typed pattern, as a
// call-shaped pattern writes one) is an error.
func (t *galaASTTransformer) argContent(a argSrc) (argValue, error) {
	if !a.node.Ok() {
		exprCtx, lambdaCtx, spread, err := extractArgContent(a.ctx)
		if err != nil {
			return argValue{}, err
		}
		if lambdaCtx != nil {
			return argValue{lambda: t.lambdaOf(lambdaCtx)}, nil
		}
		return argValue{expr: t.exprOf(exprCtx), spread: spread}, nil
	}
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

// exprOf is the expression ctx, on the native tree when the walk reads it
// (nativeNodeOf); the zero exprSrc for a nil ctx.
func (t *galaASTTransformer) exprOf(ctx grammar.IExpressionContext) exprSrc {
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_expression); ok {
		return exprSrc{node: n}
	}
	if ctx == nil {
		return exprSrc{}
	}
	return exprSrc{ctx: ctx}
}

// solePostfix is the postfix expression e is, with no operator around it
// (barePostfix), and the zero postfixSrc when e is something else.
func (e exprSrc) solePostfix() postfixSrc {
	if !e.ok() {
		return postfixSrc{}
	}
	return patExpr{exprSrc: e}.postfix(true)
}

// hintKey is p's context, the key a hint pushed for p's value takes (see
// expectedArgTypeStack.pushFor); nil for the zero postfixSrc.
func (p postfixSrc) hintKey(t *galaASTTransformer) *grammar.PostfixExprContext {
	if p.node.Ok() {
		owner, _ := t.native.Ctx(p.node).(*grammar.PostfixExprContext)
		return owner
	}
	return p.ctx
}

// hasTypeArgList reports whether c is written with type arguments and their
// expression list.
func (c callPat) hasTypeArgList() bool {
	if c.typeArgs.ctx == nil && !c.typeArgs.node.Ok() {
		return false
	}
	return indexSrc{c.typeArgs}.hasList()
}

// alone reports whether p is its primary expression only, with no suffix and
// no match.
func (p postfixSrc) alone() bool {
	if p.node.Ok() {
		return p.node.ChildCount() == 1
	}
	return p.ctx.GetChildCount() == 1
}

// parenthesized is the first element of e when e is exactly a parenthesized
// expression list — `(e)` or a tuple literal (parenthesizedList) — and the
// number of its elements; 0 when e is no such list.
// Unlike patExpr.parenthesized, it collects no elements: it runs on every
// value lowered against a slot.
func (t *galaASTTransformer) parenthesized(e exprSrc) (first exprSrc, n int) {
	if !e.node.Ok() {
		list := t.parenthesizedList(e.ctx)
		if list == nil {
			return exprSrc{}, 0
		}
		exprs := list.AllExpression()
		if len(exprs) == 0 {
			return exprSrc{}, 0
		}
		return antlrExpr(exprs[0]), len(exprs)
	}
	p := e.solePostfix()
	if !p.ok() || !p.alone() {
		return exprSrc{}, 0
	}
	for el := range galaparser.NativeKids(p.tupleListNode(), native.GalaKIND_expression) {
		if n == 0 {
			first = nativeExpr(el)
		}
		n++
	}
	return first, n
}

// grouped is the expression of e when e is exactly `(x)` (groupedExpression),
// and the zero exprSrc otherwise.
func (t *galaASTTransformer) grouped(e exprSrc) exprSrc {
	if first, n := t.parenthesized(e); n == 1 {
		return first
	}
	return exprSrc{}
}

// lambda is the lambda e is, with no suffix applied to it
// (findLambdaInExpression), and the zero lambdaSrc otherwise.
func (t *galaASTTransformer) lambdaIn(e exprSrc) lambdaSrc {
	if e.node.Ok() {
		p := e.solePostfix()
		if !p.ok() || p.hasSuffixes() {
			return lambdaSrc{}
		}
		if l := p.primaryExprNode().LambdaExpression(); l.Ok() {
			return lambdaSrc{node: l}
		}
		return lambdaSrc{}
	}
	if l := t.findLambdaInExpression(e.ctx); l != nil {
		return t.lambdaOf(l)
	}
	return lambdaSrc{}
}

// partialSrc is a partial-function literal, `{ case ... }`.
type partialSrc struct {
	ctx  *grammar.PartialFunctionLiteralContext
	node native.GalaNodeRef
}

// ok reports whether f is a partial function, not the zero partialSrc.
func (f partialSrc) ok() bool { return f.ctx != nil || f.node.Ok() }

// antlr is f's context, which its lowering (transformPartialFunctionLiteral)
// reads: the crossing from its node.
func (f partialSrc) antlr(t *galaASTTransformer) *grammar.PartialFunctionLiteralContext {
	if f.node.Ok() {
		return crossTo[*grammar.PartialFunctionLiteralContext](t, f.node)
	}
	return f.ctx
}

// partialIn is the partial function e's sole operand starts from, whatever
// suffixes follow it (findPartialFunctionInExpression), and the zero
// partialSrc when it starts from anything else.
func (t *galaASTTransformer) partialIn(e exprSrc) partialSrc {
	if e.node.Ok() {
		p := e.solePostfix()
		if !p.ok() {
			return partialSrc{}
		}
		return partialSrc{node: p.primaryExprNode().PartialFunctionLiteral().GalaNodeRef}
	}
	return partialSrc{ctx: t.findPartialFunctionInExpression(e.ctx)}
}

// barePartial is the partial function e is, with nothing applied to it
// (barePartialFunction), and the zero partialSrc otherwise.
func (t *galaASTTransformer) barePartial(e exprSrc) partialSrc {
	if p := e.solePostfix(); !p.ok() || !p.alone() {
		return partialSrc{}
	}
	return t.partialIn(e)
}

// ifIn is the if-expression e is, with no suffix applied to it
// (findIfExpressionInExpression), and the zero ifExprSrc otherwise.
func (t *galaASTTransformer) ifIn(e exprSrc) ifExprSrc {
	if e.node.Ok() {
		p := e.solePostfix()
		if !p.ok() || p.hasSuffixes() {
			return ifExprSrc{}
		}
		return ifExprSrc{node: p.primaryExprNode().IfExpression().GalaNodeRef}
	}
	if i := t.findIfExpressionInExpression(e.ctx); i != nil {
		return t.ifExprOf(i)
	}
	return ifExprSrc{}
}

// ok reports whether e is an if-expression, not the zero ifExprSrc.
func (e ifExprSrc) ok() bool { return e.ctx != nil || e.node.Ok() }

// bareMatch is the postfix expression of e when e is a match with no operator
// around it (bareMatchPostfix), and the zero postfixSrc otherwise.
func (t *galaASTTransformer) bareMatch(e exprSrc) postfixSrc {
	if e.node.Ok() {
		if p := e.solePostfix(); p.ok() && p.hasCases() {
			return p
		}
		return postfixSrc{}
	}
	if p := t.bareMatchPostfix(e.ctx); p != nil {
		return postfixSrc{ctx: p}
	}
	return postfixSrc{}
}

// antlr is p's context, which the match lowering
// (transformPostfixMatchExpressionAgainst) reads: the crossing from its node.
func (p postfixSrc) antlr(t *galaASTTransformer) *grammar.PostfixExprContext {
	if p.node.Ok() {
		return crossTo[*grammar.PostfixExprContext](t, p.node)
	}
	return p.ctx
}

// countPlaceholders is the number of `_` placeholders written in e outside any
// lambda and case clause (countPlaceholderUnderscoresInExpr).
func countPlaceholders(e exprSrc) int {
	if !e.node.Ok() {
		return countPlaceholderUnderscoresInExpr(e.ctx)
	}
	return countPlaceholdersNative(e.node)
}

// countPlaceholdersNative is countPlaceholderUnderscoresInTree of the native
// node n.
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

// escapesAt is escapesConstruct of the subtree at n, on its tree.
func (t *galaASTTransformer) escapesAt(n srcNode) bool {
	if n.node.Ok() {
		return t.cachedEscape(n.node, func() bool { return escapesNative(n.node, false) })
	}
	return escapesConstruct(t, n.ctx)
}

// matchEscapes reports whether a case clause of p, a match, escapesConstruct.
func (t *galaASTTransformer) matchEscapes(p postfixSrc) bool {
	if !p.node.Ok() {
		return escapesConstruct(t, p.ctx.AllCaseClause()...)
	}
	for c := range galaparser.NativeKids(p.node, native.GalaKIND_caseClause) {
		if t.escapesAt(srcNode{node: c}) {
			return true
		}
	}
	return false
}
