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
// A postfix expression with a match is lowered on its tree too (lowerMatch).

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
// (LeadingPostfixExpr(e, true)), and the zero postfixSrc when e is something
// else.
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

// grouped is the expression of e when e is exactly `(x)` (groupedExpression),
// and the zero exprSrc otherwise.
func (t *galaASTTransformer) grouped(e exprSrc) exprSrc {
	if first, n := t.parenthesized(e); n == 1 {
		return first
	}
	return exprSrc{}
}

// lambdaIn is the lambda e is, with no suffix applied to it, and the zero
// lambdaSrc otherwise.
func (t *galaASTTransformer) lambdaIn(e exprSrc) lambdaSrc {
	if p := e.solePostfix(); p.ok() && !p.hasSuffixes() {
		return p.primaryLambda(t)
	}
	return lambdaSrc{}
}

// classifyNative is classifyExpr of e, a native expression: its sole postfix
// expression is found once, and the form read off its primary's one child
// (as grouped, lambdaIn, barePartial and ifIn read it) or its match.
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
			if !suffixed {
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
	ctx  *grammar.PartialFunctionLiteralContext
	node native.GalaNodeRef
}

// ok reports whether f is a partial function, not the zero partialSrc.
func (f partialSrc) ok() bool { return f.ctx != nil || f.node.Ok() }

// partialOf is the partial function ctx, on the native tree when the walk
// reads it (nativeNodeOf).
func (t *galaASTTransformer) partialOf(ctx *grammar.PartialFunctionLiteralContext) partialSrc {
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_partialFunctionLiteral); ok {
		return partialSrc{node: n}
	}
	return partialSrc{ctx: ctx}
}

// at is where f is written.
func (f partialSrc) at() srcNode {
	if f.node.Ok() {
		return srcNode{node: f.node}
	}
	return srcNode{ctx: f.ctx}
}

// cases is f's case clauses, in order, read as a match's are (casesOf).
func (f partialSrc) cases(t *galaASTTransformer) []caseSrc {
	return t.casesOf(f.node, f.ctx)
}

// partialIn is the partial function e's sole operand starts from, whatever
// suffixes follow it, and the zero partialSrc when it starts from anything
// else.
func (t *galaASTTransformer) partialIn(e exprSrc) partialSrc {
	if p := e.solePostfix(); p.ok() {
		return p.primaryPartial(t)
	}
	return partialSrc{}
}

// barePartial is the partial function e is, with nothing applied to it,
// and the zero partialSrc otherwise.
func (t *galaASTTransformer) barePartial(e exprSrc) partialSrc {
	if p := e.solePostfix(); p.ok() && p.alone() {
		return p.primaryPartial(t)
	}
	return partialSrc{}
}

// ifIn is the if-expression e is, with no suffix applied to it, and the zero
// ifExprSrc otherwise.
func (t *galaASTTransformer) ifIn(e exprSrc) ifExprSrc {
	if p := e.solePostfix(); p.ok() && !p.hasSuffixes() {
		return p.primaryIf(t)
	}
	return ifExprSrc{}
}

// ok reports whether e is an if-expression, not the zero ifExprSrc.
func (e ifExprSrc) ok() bool { return e.ctx != nil || e.node.Ok() }

// bareMatch is the postfix expression of e when e is a match with no operator
// around it, and the zero postfixSrc otherwise.
func (t *galaASTTransformer) bareMatch(e exprSrc) postfixSrc {
	if p := e.solePostfix(); p.ok() && p.hasCases() {
		return p
	}
	return postfixSrc{}
}

// antlrPrimaryExpr is the primary expression of p, a postfix expression on
// ANTLR's tree, and nil when it has none or p has no context.
func (p postfixSrc) antlrPrimaryExpr() *grammar.PrimaryExprContext {
	if p.ctx == nil {
		return nil
	}
	pe, _ := p.ctx.PrimaryExpr().(*grammar.PrimaryExprContext)
	return pe
}

// antlrTupleList is the parenthesized expression list p's primary is, on
// ANTLR's tree, and nil when its primary is no such list.
func (p postfixSrc) antlrTupleList() *grammar.TupleExpressionListContext {
	prim := PrimaryOf(p.ctx)
	if prim == nil {
		return nil
	}
	list, _ := prim.TupleExpressionList().(*grammar.TupleExpressionListContext)
	return list
}

// primaryLambda is the lambda p's primary expression is, and the zero
// lambdaSrc when it is something else.
func (p postfixSrc) primaryLambda(t *galaASTTransformer) lambdaSrc {
	if p.node.Ok() {
		if l := p.primaryExprNode().LambdaExpression(); l.Ok() {
			return lambdaSrc{node: l}
		}
		return lambdaSrc{}
	}
	if pe := p.antlrPrimaryExpr(); pe != nil {
		if l, ok := pe.LambdaExpression().(*grammar.LambdaExpressionContext); ok {
			return t.lambdaOf(l)
		}
	}
	return lambdaSrc{}
}

// primaryPartial is the partial function p's primary expression is, and the
// zero partialSrc when it is something else.
func (p postfixSrc) primaryPartial(t *galaASTTransformer) partialSrc {
	if p.node.Ok() {
		return partialSrc{node: p.primaryExprNode().PartialFunctionLiteral().GalaNodeRef}
	}
	if pe := p.antlrPrimaryExpr(); pe != nil {
		if f, ok := pe.PartialFunctionLiteral().(*grammar.PartialFunctionLiteralContext); ok {
			return t.partialOf(f)
		}
	}
	return partialSrc{}
}

// primaryIf is the if-expression p's primary expression is, and the zero
// ifExprSrc when it is something else.
func (p postfixSrc) primaryIf(t *galaASTTransformer) ifExprSrc {
	if p.node.Ok() {
		return ifExprSrc{node: p.primaryExprNode().IfExpression().GalaNodeRef}
	}
	if pe := p.antlrPrimaryExpr(); pe != nil {
		if i, ok := pe.IfExpression().(*grammar.IfExpressionContext); ok {
			return t.ifExprOf(i)
		}
	}
	return ifExprSrc{}
}

// parenFirst is the first element of the parenthesized expression list p's
// primary is, and the number of its elements; 0 when its primary is no such
// list.
func (p postfixSrc) parenFirst() (first exprSrc, n int) {
	if p.node.Ok() {
		for el := range galaparser.NativeKids(p.tupleListNode(), native.GalaKIND_expression) {
			if n == 0 {
				first = nativeExpr(el)
			}
			n++
		}
		return first, n
	}
	list := p.antlrTupleList()
	if list == nil {
		return exprSrc{}, 0
	}
	exprs := list.AllExpression()
	if len(exprs) == 0 {
		return exprSrc{}, 0
	}
	return antlrExpr(exprs[0]), len(exprs)
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
// It is a scan ahead of the lowering, so it reads the clauses without
// reporting them to the onNativeCase hook (nativeCases) and stops at the
// first that escapes.
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
