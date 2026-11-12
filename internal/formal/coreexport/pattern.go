package coreexport

import "martianoff/gala/internal/parser/native"

// The grammar writes a pattern as an expression; the exporter reads the
// expression's shape as a pattern, binding nothing and resolving nothing.

func (e *exporter) pattern(p native.PatternNode) Node {
	if ep, ok := p.AsExpressionPattern(); ok {
		return e.patExpr(ep.Expression().GalaNodeRef)
	}
	if _, ok := p.AsRestPattern(); ok {
		return e.unsupported(UnsupportedRestPattern, p.GalaNodeRef)
	}
	return e.unsupported(UnsupportedTypedPattern, p.GalaNodeRef)
}

// patExpr reads an expression as a pattern. Only single-operand chains of the
// binary rules lead to one: an operator makes it an expression pattern.
func (e *exporter) patExpr(ref native.GalaNodeRef) Node {
	switch ref.Kind() {
	case native.GalaKIND_unaryExpr:
		return e.patUnary(native.UnaryExprNode{GalaNodeRef: ref})
	case native.GalaKIND_postfixExpr:
		return e.patPostfix(native.PostfixExprNode{GalaNodeRef: ref})
	}
	// expression, orExpr .. multiplicativeExpr
	if ref.ChildCount() != 1 || ref.Child(0).IsToken() {
		return e.unsupported(UnsupportedExprPattern, ref)
	}
	return e.patExpr(ref.Child(0).Node())
}

func (e *exporter) patUnary(u native.UnaryExprNode) Node {
	if !u.UnaryOp().Ok() {
		return e.patExpr(u.PostfixExpr().GalaNodeRef)
	}
	if lit, ok := intOperand(u.UnaryExpr()); ok && u.UnaryOp().GetText() == "-" {
		value := of(e, &Unary{Op: "-", Operand: e.literal(lit)}, u.GalaNodeRef)
		return of(e, &PatLit{Value: value}, u.GalaNodeRef)
	}
	return e.unsupported(UnsupportedExprPattern, u.GalaNodeRef)
}

// intOperand is u's integer literal when u is nothing else.
func intOperand(u native.UnaryExprNode) (native.LiteralNode, bool) {
	p := u.PostfixExpr()
	if !p.Ok() || p.PostfixSuffix(0).Ok() || p.MATCH().Ok() {
		return native.LiteralNode{}, false
	}
	lit := p.PrimaryExpr().Primary().Literal()
	return lit, lit.Ok() && lit.INT_LIT().Ok()
}

// patPostfix reads `callee(args)` as an extractor pattern, the callee a name
// or a chain of selects on one, and a postfix expression without suffixes as
// its primary.
func (e *exporter) patPostfix(p native.PostfixExprNode) Node {
	suffixes := p.AllPostfixSuffix()
	switch {
	case p.MATCH().Ok():
		return e.unsupported(UnsupportedExprPattern, p.GalaNodeRef)
	case len(suffixes) == 0:
		return e.patPrimary(p.PrimaryExpr())
	case !p.PrimaryExpr().Primary().Identifier().Ok():
		return e.unsupported(UnsupportedExprPattern, p.GalaNodeRef)
	}
	first := p.StartIndex()
	callee := e.primaryExpr(p.PrimaryExpr())
	last := len(suffixes) - 1
	for _, s := range suffixes[:last] {
		if !s.Identifier().Ok() {
			return e.unsupported(UnsupportedExprPattern, p.GalaNodeRef)
		}
		callee = at(e, &Select{Target: callee, Name: s.Identifier().GetText()}, first, s.StopIndex())
	}
	switch call := suffixes[last]; {
	case call.Identifier().Ok():
		return e.unsupported(UnsupportedSelectPattern, p.GalaNodeRef)
	case call.ExpressionList().Ok():
		return e.unsupported(UnsupportedExprPattern, p.GalaNodeRef)
	default:
		return of(e, &PatExtract{Callee: callee, Args: mapNodes(call.ArgumentList().AllArgument(), e.patArg)},
			p.GalaNodeRef)
	}
}

func (e *exporter) patArg(a native.ArgumentNode) Node {
	if bad := e.unsupportedArg(a); bad != nil {
		return bad
	}
	return e.pattern(a.Pattern())
}

func (e *exporter) patPrimary(p native.PrimaryExprNode) Node {
	pr := p.Primary()
	switch {
	case !pr.Ok(), pr.CompositeLiteral().Ok(), pr.Signature().Ok():
		return e.unsupported(UnsupportedExprPattern, p.GalaNodeRef)
	case pr.Identifier().Ok():
		if name := pr.Identifier().GetText(); name != "_" {
			return of(e, &PatName{Name: name}, pr.GalaNodeRef)
		}
		return of(e, &PatWild{}, pr.GalaNodeRef)
	case pr.Literal().Ok() && pr.Literal().INTERPOLATED_STRING().Ok():
		// Not a literal: its value is computed.
		return e.unsupported(UnsupportedExprPattern, p.GalaNodeRef)
	case pr.Literal().Ok():
		value := e.literal(pr.Literal())
		if _, bad := value.(*Unsupported); bad {
			return value
		}
		return of(e, &PatLit{Value: value}, pr.GalaNodeRef)
	}
	elems, bad := e.parenthesised(pr)
	switch {
	case bad != nil:
		return bad
	case len(elems) == 1:
		return e.unsupported(UnsupportedParenPattern, pr.GalaNodeRef)
	}
	return of(e, &PatTuple{Elems: mapNodes(elems, func(x native.ExpressionNode) Node {
		return e.patExpr(x.GalaNodeRef)
	})}, pr.GalaNodeRef)
}
