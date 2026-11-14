package analyzer

import (
	"cmp"
	"slices"
	"strings"

	"martianoff/gala/internal/interpolation"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// Receiver effects: what the body of a method with a pointer receiver does
// with that receiver, recorded in MethodMetadata.ReceiverEffects. A call of
// such a method on a value that cannot be addressed runs on a copy, which is
// an error when the method can mutate its receiver (GALA-E0110). Whether it
// can depends on the types of the fields it reaches and on the methods it
// calls, which may live in other files and packages, so the analyzer records
// the uses here, by name, and the transformer judges them at the call.
//
// Every occurrence of the receiver's name in the body counts, so a local or a
// lambda parameter that shadows it adds uses too: a shadowing name can only
// make the method look like it mutates, never the other way round.

// nativeReceiverEffects lists the uses of the receiver recv the body of fn
// makes beyond reading it (see transpiler.ReceiverEffect).
func nativeReceiverEffects(fn native.FunctionDeclarationNode, recv string) []transpiler.ReceiverEffect {
	if recv == "" || recv == "_" {
		return nil
	}
	var effects []transpiler.ReceiverEffect
	if body := fn.Block(); body.Ok() {
		effects = receiverEffectsIn(body.GalaNodeRef, recv, effects)
	}
	if body := fn.Expression(); body.Ok() {
		effects = receiverEffectsIn(body.GalaNodeRef, recv, effects)
	}
	// Sorted, so that the metadata is stable and duplicates are adjacent.
	slices.SortFunc(effects, func(a, b transpiler.ReceiverEffect) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), slices.Compare(a.Path, b.Path), cmp.Compare(a.Method, b.Method))
	})
	return slices.CompactFunc(effects, func(a, b transpiler.ReceiverEffect) bool {
		return a.Kind == b.Kind && a.Method == b.Method && slices.Equal(a.Path, b.Path)
	})
}

// receiverEffectsIn appends the effects of the uses of recv under root,
// those inside interpolated strings included.
func receiverEffectsIn(root native.GalaNodeRef, recv string, effects []transpiler.ReceiverEffect) []transpiler.ReceiverEffect {
	root.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_postfixExpr:
			if eff, ok := receiverUse(native.PostfixExprNode{GalaNodeRef: n}, recv); ok {
				effects = append(effects, eff)
			}
		case native.GalaKIND_literal:
			effects = interpolatedReceiverEffects(native.LiteralNode{GalaNodeRef: n}, recv, effects)
		}
		return true
	})
	return effects
}

// interpolatedReceiverEffects appends the effects of the uses of recv in the
// expressions an interpolated or format string literal embeds, which are not
// part of the tree. An expression that does not parse counts as an escape.
func interpolatedReceiverEffects(lit native.LiteralNode, recv string, effects []transpiler.ReceiverEffect) []transpiler.ReceiverEffect {
	tok := lit.INTERPOLATED_STRING()
	if !tok.Ok() {
		tok = lit.FORMAT_STRING()
	}
	if !tok.Ok() {
		return effects
	}
	raw := tok.GetText()
	if len(raw) < 3 {
		return effects
	}
	for _, part := range interpolation.Split(raw[2:len(raw)-1], raw[0] == 'f') {
		if part.IsLiteral || !strings.Contains(part.Text, recv) {
			continue
		}
		expr, errs := galaparser.ParseExpressionTree(part.Text)
		if len(errs) > 0 || !expr.Ok() {
			effects = append(effects, transpiler.ReceiverEffect{Kind: transpiler.ReceiverEscape})
			continue
		}
		effects = receiverEffectsIn(expr.GalaNodeRef, recv, effects)
	}
	return effects
}

// receiverUse classifies a postfix expression rooted at recv: `r.a.b` read as
// a value is no effect; `r.a.M(...)` is a call of M on [a]; `r.a = x`,
// `r.a++` and `&r.a` write [a]; `r` itself as a value is an escape, except as
// an operand of a comparison or dereferenced (`*r`), both of which read it.
// ok is false for an expression not rooted at recv, or one that only reads.
func receiverUse(pe native.PostfixExprNode, recv string) (transpiler.ReceiverEffect, bool) {
	if !isReceiverPrimary(pe.PrimaryExpr(), recv) {
		return transpiler.ReceiverEffect{}, false
	}
	return placeUse(pe, nil, false)
}

// placeUse classifies the postfix expression pe, which applies its suffixes
// to the place path of the receiver (dereferenced, when deref), as
// receiverUse does. A place in parentheses (`(*r).a = x`) is classified as
// the expression the parentheses are part of.
func placeUse(pe native.PostfixExprNode, path []string, deref bool) (transpiler.ReceiverEffect, bool) {
	path = slices.Clip(path)
	for _, s := range pe.AllPostfixSuffix() {
		switch {
		case s.Identifier().Ok():
			path = append(path, s.Identifier().GetText())
		case s.ExpressionList().Ok():
			path = append(path, transpiler.ReceiverIndex)
		default:
			// A call: of the method the last selector names, past any type
			// arguments (`r.a.M[int](x)`). What follows works on its result.
			return receiverCall(path)
		}
	}
	if pe.MATCH().Ok() && len(path) == 0 {
		return transpiler.ReceiverEffect{Kind: transpiler.ReceiverEscape}, true
	}
	place := pe.Parent() // the unaryExpr holding pe
	op := unaryOperator(place.Parent())
	if len(path) == 0 && !deref && op == "*" {
		place, deref = place.Parent(), true
		op = unaryOperator(place.Parent())
	}
	if op == "&" {
		return transpiler.ReceiverEffect{Kind: transpiler.ReceiverWrite, Path: path}, true
	}
	whole, binaryOperand := expressionOf(place)
	if outer, ok := parenthesized(whole); ok {
		return placeUse(outer, path, deref)
	}
	if whole.Ok() && isAssignedPlace(whole) {
		return transpiler.ReceiverEffect{Kind: transpiler.ReceiverWrite, Path: path}, true
	}
	switch {
	case len(path) > 0 && path[len(path)-1] != transpiler.ReceiverIndex:
		// A field read, or a method value (`r.Bump`), bound to the receiver
		// and callable later: which one is up to the transformer.
		return receiverCall(path)
	case len(path) > 0 || deref || binaryOperand:
		return transpiler.ReceiverEffect{}, false // a read: a copy of the place
	}
	return transpiler.ReceiverEffect{Kind: transpiler.ReceiverEscape}, true
}

// parenthesized is the postfix expression whose primary is `(expr)`, when
// expr is the whole of those parentheses.
func parenthesized(expr native.GalaNodeRef) (native.PostfixExprNode, bool) {
	if !expr.Ok() {
		return native.PostfixExprNode{}, false
	}
	list := expr.Parent()
	if !list.Ok() || list.Kind() != native.GalaKIND_tupleExpressionList || list.ChildCount() != 1 {
		return native.PostfixExprNode{}, false
	}
	primary := list.Parent()
	if !primary.Ok() || primary.Kind() != native.GalaKIND_primary {
		return native.PostfixExprNode{}, false
	}
	primaryExpr := primary.Parent()
	if !primaryExpr.Ok() || primaryExpr.Kind() != native.GalaKIND_primaryExpr {
		return native.PostfixExprNode{}, false
	}
	pe := primaryExpr.Parent()
	if !pe.Ok() || pe.Kind() != native.GalaKIND_postfixExpr {
		return native.PostfixExprNode{}, false
	}
	return native.PostfixExprNode{GalaNodeRef: pe}, true
}

// receiverCall is the effect of a call that ends path: of the method its last
// selector names, on the place before it. A call with no selector before it
// (`r(...)`, `r.items[0](x)`) calls a function value it reads.
func receiverCall(path []string) (transpiler.ReceiverEffect, bool) {
	i := len(path) - 1
	for i >= 0 && path[i] == transpiler.ReceiverIndex {
		i--
	}
	if i < 0 {
		if len(path) == 0 {
			return transpiler.ReceiverEffect{Kind: transpiler.ReceiverEscape}, true
		}
		return transpiler.ReceiverEffect{}, false
	}
	return transpiler.ReceiverEffect{Kind: transpiler.ReceiverCall, Path: path[:i:i], Method: path[i]}, true
}

// isReceiverPrimary reports whether pe is the bare name recv.
func isReceiverPrimary(pe native.PrimaryExprNode, recv string) bool {
	p := pe.Primary()
	if !p.Ok() || p.ChildCount() != 1 {
		return false
	}
	id := p.Identifier()
	return id.Ok() && id.GetText() == recv
}

// unaryOperator is the operator of n when n is a unary expression that
// applies one (`*x`, `&x`), and "" otherwise.
func unaryOperator(n native.GalaNodeRef) string {
	if !n.Ok() || n.Kind() != native.GalaKIND_unaryExpr {
		return ""
	}
	if op := (native.UnaryExprNode{GalaNodeRef: n}).UnaryOp(); op.Ok() {
		return op.GetText()
	}
	return ""
}

// expressionOf climbs from the unary expression n to the expression it is
// the whole of. binaryOperand is true when n is instead an operand of a
// binary operator, and then whole is no node.
func expressionOf(n native.GalaNodeRef) (whole native.GalaNodeRef, binaryOperand bool) {
	for n.Ok() {
		parent := n.Parent()
		if !parent.Ok() {
			return native.GalaNodeRef{}, false
		}
		switch parent.Kind() {
		case native.GalaKIND_multiplicativeExpr, native.GalaKIND_additiveExpr,
			native.GalaKIND_relationalExpr, native.GalaKIND_equalityExpr,
			native.GalaKIND_andExpr, native.GalaKIND_orExpr:
			if parent.ChildCount() != 1 {
				return native.GalaNodeRef{}, true
			}
		case native.GalaKIND_expression:
			return parent, false
		default:
			return native.GalaNodeRef{}, false
		}
		n = parent
	}
	return native.GalaNodeRef{}, false
}

// isAssignedPlace reports whether the expression expr is assigned to: on the
// left of an assignment, or incremented or decremented.
func isAssignedPlace(expr native.GalaNodeRef) bool {
	parent := expr.Parent()
	if !parent.Ok() {
		return false
	}
	switch parent.Kind() {
	case native.GalaKIND_incDecStmt:
		return true
	case native.GalaKIND_expressionList:
		assign := parent.Parent()
		if !assign.Ok() || assign.Kind() != native.GalaKIND_assignment {
			return false
		}
		lhs := native.AssignmentNode{GalaNodeRef: assign}.ExpressionList(0)
		return lhs.Ok() && lhs.Index() == parent.Index()
	}
	return false
}
