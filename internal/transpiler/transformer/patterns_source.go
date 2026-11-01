package transformer

import (
	"go/ast"
	"slices"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// A match's case clauses and their patterns as the pattern lowering reads
// them, on galop's native tree (transformPatternWithType,
// transformExpressionPatternWithType, coverageOf, sealedViewForCases, ...).
//
// A pattern is an expression read as a pattern: its shape (a binding, a
// literal, a tuple, a call, alternatives) is read here. What a pattern holds
// that is a value — a literal compared by ==, an extractor's head, explicit
// type arguments (written as expressions) — is lowered as an expression
// (lowerPatExpr, lowerPatternHead), as is a typed pattern's type (lowerType).
//
// The readers dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// patKind is which alternative of rule pattern a pattern is.
type patKind uint8

const (
	patKindNone    patKind = iota // no pattern: a lambda argument, a field left out
	patKindRest                   // `p...`
	patKindExpr                   // an expression read as a pattern
	patKindTyped                  // `name: Type`
	patKindUnknown                // a rule alternative the lowering does not know
)

// patSrc is a pattern: its node, of kind restPattern, expressionPattern or
// typedPattern. The zero patSrc is no pattern.
type patSrc struct {
	node native.GalaNodeRef
}

// nativePat is the pattern n, or the zero patSrc when n is no node.
func nativePat(n native.GalaNodeRef) patSrc {
	if !n.Ok() {
		return patSrc{}
	}
	return patSrc{node: n}
}

// ok reports whether p is a pattern.
func (p patSrc) ok() bool { return p.node.Ok() }

// kind is p's alternative.
func (p patSrc) kind() patKind {
	if !p.node.Ok() {
		return patKindNone
	}
	switch p.node.Kind() {
	case native.GalaKIND_restPattern:
		return patKindRest
	case native.GalaKIND_expressionPattern:
		return patKindExpr
	case native.GalaKIND_typedPattern:
		return patKindTyped
	}
	return patKindUnknown
}

// ruleName is the grammar rule p is a node of, for the diagnostic of a
// pattern the lowering does not know (patKindUnknown).
func (p patSrc) ruleName() string { return native.GalaRuleNames[p.node.Rule()] }

// text is p's tokens' text, joined.
func (p patSrc) text() string { return galaparser.NativeText(p.node) }

// at is where p is written.
func (p patSrc) at() srcNode { return srcNode{node: p.node} }

// expr is the expression of p, a rest or an expression pattern.
func (p patSrc) expr() patExpr {
	if !p.node.Ok() {
		return patExpr{}
	}
	var e native.ExpressionNode
	switch p.node.Kind() {
	case native.GalaKIND_restPattern:
		e = native.RestPatternNode{GalaNodeRef: p.node}.Expression()
	case native.GalaKIND_expressionPattern:
		e = native.ExpressionPatternNode{GalaNodeRef: p.node}.Expression()
	}
	if e.Ok() {
		return patExpr{exprSrc: exprSrc{node: e.GalaNodeRef}}
	}
	return patExpr{}
}

// typed is p's node as a typed pattern.
func (p patSrc) typed() native.TypedPatternNode { return native.TypedPatternNode{GalaNodeRef: p.node} }

// typedName is the name p, a typed pattern, binds.
func (p patSrc) typedName() string { return p.typed().Identifier().GetText() }

// typedType is the type p, a typed pattern, tests for.
func (p patSrc) typedType() typeSrc { return nativeType(p.typed().Type_()) }

// patExpr is an expression read as a pattern: an expression node or, for an
// alternative, the multiplicativeExpr node it is. The zero patExpr is no
// expression. Its ok, at and text are exprSrc's.
type patExpr struct {
	exprSrc
}

// sourceText is e as written, whitespace included (transpiler.SourceText).
func (e patExpr) sourceText() string { return e.at().sourceText() }

// start is the line and column of e's first token.
func (e patExpr) start() (line, col int) { return e.at().start() }

// line is the line of e's first token.
func (e patExpr) line() int { l, _ := e.start(); return l }

// col is the column of e's first token.
func (e patExpr) col() int { _, c := e.start(); return c }

// lowerPatExpr lowers e, a pattern's value, as an expression. An alternative
// is lowered as the operand it is.
func (t *galaASTTransformer) lowerPatExpr(e patExpr) (ast.Expr, error) {
	if !e.isAlternative() {
		return t.lowerExpr(e.exprSrc)
	}
	t.trackNativePosition(e.node)
	return t.lowerOperatorsNative(e.node)
}

// isAlternative reports whether e is a native alternative: one operand of a
// `|` chain, a multiplicativeExpr node.
func (e patExpr) isAlternative() bool {
	return e.node.Ok() && e.node.Kind() == native.GalaKIND_multiplicativeExpr
}

// valueShape is the shape the native walk reports for e, a pattern's value
// compared by == (see nativeShape): "alternative" for one alternative of
// several, "pattern" otherwise.
func (e patExpr) valueShape() string {
	if e.isAlternative() {
		return "alternative"
	}
	return "pattern"
}

// patChain is the precedence chain of an expression, from the expression
// down to the postfix expression of an operand.
var patChain = [...]native.GalaKind{
	native.GalaKIND_expression,
	native.GalaKIND_orExpr,
	native.GalaKIND_andExpr,
	native.GalaKIND_equalityExpr,
	native.GalaKIND_relationalExpr,
	native.GalaKIND_additiveExpr,
	native.GalaKIND_multiplicativeExpr,
	native.GalaKIND_unaryExpr,
	native.GalaKIND_postfixExpr,
}

// nativeOperand walks the precedence chain from n, a node on it, down to the
// node of kind target of n's first operand, and no node when an operand is
// missing or a unary operator intervenes. With sole set, every level must
// hold exactly one operand.
func nativeOperand(n native.GalaNodeRef, target native.GalaKind, sole bool) native.GalaNodeRef {
	if !n.Ok() {
		return n
	}
	i, j := slices.Index(patChain[:], n.Kind()), slices.Index(patChain[:], target)
	if i < 0 || j < i {
		return native.GalaNodeRef{}
	}
	for ; i < j; i++ {
		var ok bool
		if sole {
			n, ok = galaparser.OnlyNativeKid(n, patChain[i+1])
		} else {
			n, ok = firstNativeKid(n, patChain[i+1])
		}
		if !ok {
			return native.GalaNodeRef{}
		}
	}
	return n
}

// firstNativeKid is n's first child node of kind, and false when it has none.
func firstNativeKid(n native.GalaNodeRef, kind native.GalaKind) (native.GalaNodeRef, bool) {
	for c := range galaparser.NativeKids(n, kind) {
		return c, true
	}
	return native.GalaNodeRef{}, false
}

// postfix is the postfix expression e is (sole) or starts with; the zero
// postfixSrc when there is none.
func (e patExpr) postfix(sole bool) postfixSrc {
	return postfixSrc{node: nativeOperand(e.node, native.GalaKIND_postfixExpr, sole)}
}

// patAdditive is the additive chain of a pattern's sole operand: its operands
// and the operators between them.
type patAdditive struct {
	operands []patExpr
	ops      []string
	at       srcNode
}

// soleAdditive is the additive chain e is, and false when `||`, `&&` or a
// comparison joins operands above it. Each operand of several is an
// expression of its own, its multiplicativeExpr node.
func (e patExpr) soleAdditive() (patAdditive, bool, error) {
	if !e.node.Ok() {
		return patAdditive{}, false, nil
	}
	if e.node.Kind() == native.GalaKIND_multiplicativeExpr {
		return patAdditive{operands: []patExpr{e}, at: e.at()}, true, nil
	}
	add := nativeOperand(e.node, native.GalaKIND_additiveExpr, true)
	if !add.Ok() {
		return patAdditive{}, false, nil
	}
	out := patAdditive{at: srcNode{node: add}}
	if _, sole := galaparser.OnlyNativeKid(add, native.GalaKIND_multiplicativeExpr); sole {
		out.operands = []patExpr{e}
		return out, true, nil
	}
	for i := range add.ChildCount() {
		c := add.Child(i)
		if m := c.Node(); m.Ok() && m.Kind() == native.GalaKIND_multiplicativeExpr {
			out.operands = append(out.operands, patExpr{exprSrc: exprSrc{node: m}})
		} else if tok := c.Terminal(); tok.Ok() {
			out.ops = append(out.ops, tok.GetText())
		}
	}
	return out, true, nil
}

// barUnderOperator is where an additive chain of e that holds a `|` starts,
// when `||`, `&&` or a comparison joins several operands above it, and false
// otherwise.
func (e patExpr) barUnderOperator() (srcNode, bool) {
	if !e.node.Ok() {
		return srcNode{}, false
	}
	var found native.GalaNodeRef
	or := (native.ExpressionNode{GalaNodeRef: e.node}).OrExpr()
	if !or.Ok() {
		return srcNode{}, false
	}
	or.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_orExpr, native.GalaKIND_andExpr, native.GalaKIND_equalityExpr, native.GalaKIND_relationalExpr:
			return !found.Ok()
		case native.GalaKIND_additiveExpr:
			for i := 1; i < n.ChildCount() && !found.Ok(); i += 2 {
				if tok := n.Child(i).Terminal(); tok.Ok() && tok.GetText() == "|" {
					found = n
				}
			}
		}
		return false
	})
	return srcNode{node: found}, found.Ok()
}

// parenthesized is the elements of e when e is exactly a parenthesized list
// `(...)`, and single reports whether the list is one expression with no
// trailing comma: a parenthesized pattern, not a tuple.
func (e patExpr) parenthesized(t *galaASTTransformer) (elems []patExpr, single, ok bool) {
	p := e.postfix(true)
	if !p.ok() || !p.alone() {
		return nil, false, false
	}
	list := p.tupleListNode()
	if !list.Ok() {
		return nil, false, false
	}
	return nativeExprs(list), list.ChildCount() == 1, true
}

// tupleElems is the elements of the parenthesized list e starts with, and
// false when it starts with none.
func (e patExpr) tupleElems() ([]patExpr, bool) {
	p := e.postfix(false)
	if !p.ok() {
		return nil, false
	}
	list := p.tupleListNode()
	return nativeExprs(list), list.Ok()
}

// nativeExprs is the expression children of n.
func nativeExprs(n native.GalaNodeRef) []patExpr {
	var out []patExpr
	for c := range galaparser.NativeKids(n, native.GalaKIND_expression) {
		out = append(out, patExpr{exprSrc: exprSrc{node: c}})
	}
	return out
}

// identifier is the name when e is a bare identifier (patternIdentifier),
// and "" for anything else — a literal, a qualified name such as
// `math.MaxInt8`, or an operator expression.
func (e patExpr) identifier() string {
	p := e.postfix(true)
	if !p.ok() || p.hasSuffixes() || p.hasCases() {
		return ""
	}
	return p.primaryIdent()
}

// callPat is a call-shaped pattern: `Ctor(args)`, `Ctor[T](args)`, or, read
// by qualifiedCallPattern, `pkg.Ctor(args)` and `pkg.Ctor[T](args)`.
type callPat struct {
	// head is the postfix expression whose primary is the constructor, or
	// for a qualified pattern the package.
	head postfixSrc
	// ctor is a qualified pattern's constructor name.
	ctor     string
	args     argsSrc
	typeArgs suffixSrc // the type arguments' suffix, the zero suffixSrc when none
}

// lowerPatternHead lowers the primary expression of c's head, its
// constructor or, for a qualified pattern, its package.
func (t *galaASTTransformer) lowerPatternHead(c callPat) (ast.Expr, error) {
	t.nativeShape(c.head.node, "extractor")
	return t.lowerPrimaryOf(c.head)
}

// callPattern reads e as an unqualified call pattern
// (getCallPatternWithTypeArgsFromExpression), and false when it is none.
func (e patExpr) callPattern() (callPat, bool) {
	p := e.postfix(true)
	if !p.ok() {
		return callPat{}, false
	}
	suffixes := p.suffixes()
	var typeArgs, call suffixSrc
	switch len(suffixes) {
	case 1:
		call = suffixes[0]
	case 2:
		typeArgs, call = suffixes[0], suffixes[1]
		if typeArgs.childCount() < 2 || typeArgs.opener() != "[" {
			return callPat{}, false
		}
	default:
		return callPat{}, false
	}
	if call.childCount() < 2 || call.opener() != "(" || !p.hasPrimaryExpr() {
		return callPat{}, false
	}
	return callPat{head: p, args: call.args(), typeArgs: typeArgs}, true
}

// qualifiedCallPattern reads e as a package-qualified call pattern,
// `pkg.Ctor(args)` or `pkg.Ctor[T](args)`, and false when it is none.
func (e patExpr) qualifiedCallPattern() (callPat, bool) {
	p := e.postfix(true)
	if !p.ok() {
		return callPat{}, false
	}
	suffixes := p.suffixes()
	if len(suffixes) != 2 && len(suffixes) != 3 {
		return callPat{}, false
	}
	member, call := suffixes[0], suffixes[len(suffixes)-1]
	ctor := member.ident()
	if ctor == "" {
		return callPat{}, false
	}
	var typeArgs suffixSrc
	if len(suffixes) == 3 {
		if typeArgs = suffixes[1]; typeArgs.opener() != "[" {
			return callPat{}, false
		}
	}
	if call.childCount() < 2 || call.opener() != "(" || !p.hasPrimaryExpr() {
		return callPat{}, false
	}
	return callPat{head: p, ctor: ctor, args: call.args(), typeArgs: typeArgs}, true
}

// callShape reads e as a call-shaped pattern of any qualification
// (patternCallShape): the bare constructor name (the last selector, or the
// primary identifier), the call's arguments, and whether e has that shape.
func (e patExpr) callShape() (name string, args argsSrc, ok bool) {
	p := e.postfix(true)
	if !p.ok() {
		return "", argsSrc{}, false
	}
	if name = p.primaryIdent(); name == "" {
		return "", argsSrc{}, false
	}
	suffixes := p.suffixes()
	if len(suffixes) == 0 {
		return "", argsSrc{}, false
	}
	call := suffixes[len(suffixes)-1]
	if call.opener() != "(" {
		return "", argsSrc{}, false
	}
	prefix := suffixes[:len(suffixes)-1]
	// Optional type arguments right before the call.
	if n := len(prefix); n > 0 && prefix[n-1].opener() == "[" {
		prefix = prefix[:n-1]
	}
	// Everything else is a selector chain; its last member is the name.
	for _, s := range prefix {
		if name = s.ident(); name == "" {
			return "", argsSrc{}, false
		}
	}
	return name, call.args(), true
}

// qualifiedMember reads e as `pkg.Name`, a primary identifier and one member
// selector (as qualifiedZeroFieldCase reads it), and false otherwise.
func (e patExpr) qualifiedMember() (pkg, name string, ok bool) {
	p := e.postfix(true)
	if !p.ok() || p.hasCases() {
		return "", "", false
	}
	suffixes := p.suffixes()
	if len(suffixes) != 1 || suffixes[0].opener() != "." {
		return "", "", false
	}
	name, pkg = suffixes[0].ident(), p.primaryIdent()
	return pkg, name, name != "" && pkg != ""
}

// postfixSrc is a postfix expression. owner, set by the postfix driver
// (lowerSuffixes), is the key of the hint for the slot its value fills (see
// hintOwner).
type postfixSrc struct {
	node  native.GalaNodeRef
	owner hintOwner
}

// ok reports whether p is a postfix expression.
func (p postfixSrc) ok() bool { return p.node.Ok() }

// suffixes is p's postfix suffixes, in order.
func (p postfixSrc) suffixes() []suffixSrc { return p.appendSuffixes(nil) }

// appendSuffixes appends p's postfix suffixes, in order, to out.
func (p postfixSrc) appendSuffixes(out []suffixSrc) []suffixSrc {
	for s := range galaparser.NativeKids(p.node, native.GalaKIND_postfixSuffix) {
		out = append(out, suffixSrc{node: s})
	}
	return out
}

// hasSuffixes reports whether p has a postfix suffix.
func (p postfixSrc) hasSuffixes() bool {
	_, ok := firstNativeKid(p.node, native.GalaKIND_postfixSuffix)
	return ok
}

// hasCases reports whether p ends in a match.
func (p postfixSrc) hasCases() bool {
	_, ok := firstNativeKid(p.node, native.GalaKIND_caseClause)
	return ok
}

// primaryExprNode is p's primary expression node.
func (p postfixSrc) primaryExprNode() native.PrimaryExprNode {
	return (native.PostfixExprNode{GalaNodeRef: p.node}).PrimaryExpr()
}

// hasPrimaryExpr reports whether p has a primary expression.
func (p postfixSrc) hasPrimaryExpr() bool { return p.primaryExprNode().Ok() }

// primaryIdent is the identifier p starts from, "" when it starts from
// anything else.
func (p postfixSrc) primaryIdent() string {
	if id := p.primaryExprNode().Primary().Identifier(); id.Ok() {
		return galaparser.NativeText(id.GalaNodeRef)
	}
	return ""
}

// tupleListNode is the parenthesized list p's primary is, or no node.
func (p postfixSrc) tupleListNode() native.GalaNodeRef {
	return p.primaryExprNode().Primary().TupleExpressionList().GalaNodeRef
}

// suffixSrc is a postfix suffix: `.name`, `(args)` or `[exprs]`.
type suffixSrc struct {
	node native.GalaNodeRef
}

// childCount is the number of s's children, tokens included.
func (s suffixSrc) childCount() int { return s.node.ChildCount() }

// opener is the token s starts with: ".", "(" or "[".
func (s suffixSrc) opener() string {
	if s.node.ChildCount() == 0 {
		return ""
	}
	if tok := s.node.Child(0).Terminal(); tok.Ok() {
		return tok.GetText()
	}
	return s.node.Child(0).Node().GetText()
}

// ident is the member s selects, "" when it is no selector.
func (s suffixSrc) ident() string {
	name, _, _ := s.member()
	return name
}

// args is the arguments of s, a call suffix.
func (s suffixSrc) args() argsSrc {
	list := (native.PostfixSuffixNode{GalaNodeRef: s.node}).ArgumentList()
	if !list.Ok() {
		return argsSrc{}
	}
	out := argsSrc{at: srcNode{node: list.GalaNodeRef}}
	for a := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_argument) {
		out.list = append(out.list, argSrc{node: a})
	}
	return out
}

// lowerTypeArg lowers e, a pattern's explicit type argument, as an
// expression.
func (t *galaASTTransformer) lowerTypeArg(e exprSrc) (ast.Expr, error) {
	t.nativeShape(e.node, "type-arg")
	return t.lowerExpr(e)
}

// typeArgs is the expressions of s, a type-argument suffix, in order; nil
// when s is none or has none.
func (s suffixSrc) typeArgs() []exprSrc {
	if !s.node.Ok() {
		return nil
	}
	return slices.Collect(indexSrc{s}.exprs())
}

// argsSrc is the arguments of a call, or the sub-patterns of a call-shaped
// pattern, and at, their argument list (the zero srcNode when the call has
// none).
type argsSrc struct {
	list []argSrc
	at   srcNode
}

// argSrc is an argument of a call, or of a call-shaped pattern: a value or a
// sub-pattern, possibly named (`Field = p`), or a lambda. The zero argSrc is
// a field named sub-patterns leave out: it has no name and no pattern.
type argSrc struct {
	node native.GalaNodeRef
}

// at is where a is written.
func (a argSrc) at() srcNode { return srcNode{node: a.node} }

// name is the field a names and where the name is written, "" when a is
// positional.
func (a argSrc) name() (string, srcNode) {
	if a.node.Ok() {
		if id := (native.ArgumentNode{GalaNodeRef: a.node}).Identifier(); id.Ok() {
			return galaparser.NativeText(id.GalaNodeRef), srcNode{node: id.GalaNodeRef}
		}
	}
	return "", srcNode{}
}

// pattern is a's sub-pattern, the zero patSrc when a is a lambda or left out.
func (a argSrc) pattern() patSrc {
	if !a.node.Ok() {
		return patSrc{}
	}
	return nativePat((native.ArgumentNode{GalaNodeRef: a.node}).Pattern().GalaNodeRef)
}
