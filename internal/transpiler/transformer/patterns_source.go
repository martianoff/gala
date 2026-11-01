package transformer

import (
	"slices"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// A match's case clauses and their patterns as the pattern lowering reads
// them. Each source below is a node of ANTLR's tree (ctx) or of galop's
// native one (node), and each of its readers has an ANTLR half and a native
// half that give the same value for the same node, so a pattern is lowered
// once, the same way, whichever tree it comes from (transformPatternWithType,
// transformExpressionPatternWithType, coverageOf, sealedViewForCases, ...).
//
// A pattern is an expression read as a pattern: its shape (a binding, a
// literal, a tuple, a call, alternatives) is read here, on either tree. What a
// pattern holds that is a value — a literal compared by ==, an extractor's
// head, explicit type arguments (written as expressions) — is lowered as an
// expression, which is read on ANTLR's tree: the native readers cross to it
// with crossTo. A typed pattern's type is lowered on either tree (lowerType).
//
// The native halves dispatch with Kind() and read children through the rule
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

// patSrc is a pattern: a context of rule pattern, or its node (of kind
// restPattern, expressionPattern or typedPattern). The zero patSrc is no
// pattern.
type patSrc struct {
	ctx  grammar.IPatternContext
	node native.GalaNodeRef
}

// antlrPat is the pattern ctx, on ANTLR's tree; the zero patSrc when ctx is
// nil.
func antlrPat(ctx grammar.IPatternContext) patSrc {
	if ctx == nil {
		return patSrc{}
	}
	return patSrc{ctx: ctx}
}

// nativePat is the pattern n, or the zero patSrc when n is no node.
func nativePat(n native.GalaNodeRef) patSrc {
	if !n.Ok() {
		return patSrc{}
	}
	return patSrc{node: n}
}

// ok reports whether p is a pattern.
func (p patSrc) ok() bool { return p.ctx != nil || p.node.Ok() }

// kind is p's alternative.
func (p patSrc) kind() patKind {
	switch {
	case p.node.Ok():
		switch p.node.Kind() {
		case native.GalaKIND_restPattern:
			return patKindRest
		case native.GalaKIND_expressionPattern:
			return patKindExpr
		case native.GalaKIND_typedPattern:
			return patKindTyped
		}
		return patKindUnknown
	case p.ctx != nil:
		switch p.ctx.(type) {
		case *grammar.RestPatternContext:
			return patKindRest
		case *grammar.ExpressionPatternContext:
			return patKindExpr
		case *grammar.TypedPatternContext:
			return patKindTyped
		}
		return patKindUnknown
	}
	return patKindNone
}

// text is GetText of p.
func (p patSrc) text() string {
	if p.node.Ok() {
		return galaparser.NativeText(p.node)
	}
	return p.ctx.GetText()
}

// at is where p is written.
func (p patSrc) at() srcNode {
	if p.node.Ok() {
		return srcNode{node: p.node}
	}
	return srcNode{ctx: p.ctx}
}

// expr is the expression of p, a rest or an expression pattern.
func (p patSrc) expr() patExpr {
	if p.node.Ok() {
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
	switch c := p.ctx.(type) {
	case *grammar.RestPatternContext:
		return antlrPatExpr(c.Expression())
	case *grammar.ExpressionPatternContext:
		return antlrPatExpr(c.Expression())
	}
	return patExpr{}
}

// typed is p's node as a typed pattern.
func (p patSrc) typed() native.TypedPatternNode { return native.TypedPatternNode{GalaNodeRef: p.node} }

// typedName is the name p, a typed pattern, binds.
func (p patSrc) typedName() string {
	if p.node.Ok() {
		return p.typed().Identifier().GetText()
	}
	return p.ctx.(*grammar.TypedPatternContext).Identifier().GetText()
}

// typedType is the type p, a typed pattern, tests for.
func (p patSrc) typedType() typeSrc {
	if p.node.Ok() {
		return nativeType(p.typed().Type_())
	}
	return antlrType(p.ctx.(*grammar.TypedPatternContext).Type_())
}

// patExpr is an expression read as a pattern: an expression context on
// ANTLR's tree (an alternative's wrapper included, see wrapAsExpression), or
// on the native tree an expression node or, for an alternative, the
// multiplicativeExpr node it is and from, the expression it was split from.
// The zero patExpr is no expression. Its ok, at and text are exprSrc's.
type patExpr struct {
	exprSrc
	from native.GalaNodeRef
}

// antlrPatExpr is the expression ctx, on ANTLR's tree; the zero patExpr when
// ctx is nil.
func antlrPatExpr(ctx grammar.IExpressionContext) patExpr {
	if ctx == nil {
		return patExpr{}
	}
	return patExpr{exprSrc: exprSrc{ctx: ctx}}
}

// sourceText is e as written, whitespace included (transpiler.SourceText).
func (e patExpr) sourceText() string {
	if e.node.Ok() {
		if s := e.node.SourceText(); s != "" {
			return s
		}
		return e.text()
	}
	return transpiler.SourceText(e.ctx)
}

// start is the line and column of e's first token.
func (e patExpr) start() (line, col int) { return e.at().start() }

// line is the line of e's first token.
func (e patExpr) line() int { l, _ := e.start(); return l }

// col is the column of e's first token.
func (e patExpr) col() int { _, c := e.start(); return c }

// antlr is e as the expression lowering reads it: e's context, or its native
// node's (an alternative's wrapped as a whole expression, as on ANTLR's
// tree).
func (e patExpr) antlr(t *galaASTTransformer) grammar.IExpressionContext {
	if !e.node.Ok() {
		return e.ctx
	}
	if e.node.Kind() == native.GalaKIND_multiplicativeExpr {
		return wrapAsExpression(crossTo[*grammar.ExpressionContext](t, e.from), crossTo[*grammar.MultiplicativeExprContext](t, e.node))
	}
	return crossTo[*grammar.ExpressionContext](t, e.node)
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
// node of kind target of n's first operand (leadingAdditiveExpr and
// LeadingPostfixExpr on the native tree), and no node when an operand is
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

// postfix is the postfix expression e is (sole, getSinglePostfixExpr) or
// starts with (LeadingPostfixExpr(e, false)); the zero postfixSrc when there
// is none.
func (e patExpr) postfix(sole bool) postfixSrc {
	if e.node.Ok() {
		return postfixSrc{node: nativeOperand(e.node, native.GalaKIND_postfixExpr, sole)}
	}
	if p := LeadingPostfixExpr(e.ctx, sole); p != nil {
		return postfixSrc{ctx: p}
	}
	return postfixSrc{}
}

// patAdditive is the additive chain of a pattern's sole operand: its operands
// and the operators between them.
type patAdditive struct {
	operands []patExpr
	ops      []string
	at       srcNode
}

// soleAdditive is the additive chain e is (leadingAdditiveExpr(e, true)), and
// false when `||`, `&&` or a comparison joins operands above it. Each operand
// of several is an expression of its own (see wrapAsExpression).
func (e patExpr) soleAdditive() (patAdditive, bool, error) {
	if e.node.Ok() {
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
				out.operands = append(out.operands, patExpr{exprSrc: exprSrc{node: m}, from: e.node})
			} else if tok := c.Terminal(); tok.Ok() {
				out.ops = append(out.ops, tok.GetText())
			}
		}
		return out, true, nil
	}
	add := leadingAdditiveExpr(e.ctx, true)
	if add == nil {
		return patAdditive{}, false, nil
	}
	muls := add.AllMultiplicativeExpr()
	out := patAdditive{at: srcNode{ctx: add}}
	if len(muls) == 1 {
		out.operands = []patExpr{e}
		return out, true, nil
	}
	for i, m := range muls {
		out.operands = append(out.operands, antlrPatExpr(wrapAsExpression(e.ctx, m.(*grammar.MultiplicativeExprContext))))
		if i > 0 {
			op, err := getChildOperatorText(add, i*2-1)
			if err != nil {
				return patAdditive{}, false, err
			}
			out.ops = append(out.ops, op)
		}
	}
	return out, true, nil
}

// barUnderOperator is where an additive chain of e that holds a `|` starts,
// when `||`, `&&` or a comparison joins several operands above it, and false
// otherwise.
func (e patExpr) barUnderOperator() (srcNode, bool) {
	if !e.node.Ok() {
		if add := barUnderOperator(e.ctx); add != nil {
			return srcNode{ctx: add}, true
		}
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
// `(...)` (parenthesizedList), and single reports whether the list is one
// expression with no trailing comma: a parenthesized pattern, not a tuple.
func (e patExpr) parenthesized(t *galaASTTransformer) (elems []patExpr, single, ok bool) {
	if e.node.Ok() {
		p := postfixSrc{node: nativeOperand(e.node, native.GalaKIND_postfixExpr, true)}
		if !p.ok() || p.node.ChildCount() != 1 {
			return nil, false, false
		}
		list := p.tupleListNode()
		if !list.Ok() {
			return nil, false, false
		}
		return nativeExprs(list), list.ChildCount() == 1, true
	}
	list := t.parenthesizedList(e.ctx)
	if list == nil {
		return nil, false, false
	}
	return antlrExprs(list.AllExpression()), list.GetChildCount() == 1, true
}

// tupleElems is the elements of the parenthesized list e starts with
// (getPrimaryFromExpression's TupleExpressionList), and false when it starts
// with none.
func (e patExpr) tupleElems() ([]patExpr, bool) {
	p := e.postfix(false)
	if !p.ok() {
		return nil, false
	}
	if p.node.Ok() {
		list := p.tupleListNode()
		return nativeExprs(list), list.Ok()
	}
	prim := PrimaryOf(p.ctx)
	if prim == nil {
		return nil, false
	}
	list, ok := prim.TupleExpressionList().(*grammar.TupleExpressionListContext)
	if !ok {
		return nil, false
	}
	return antlrExprs(list.AllExpression()), true
}

// nativeExprs is the expression children of n.
func nativeExprs(n native.GalaNodeRef) []patExpr {
	var out []patExpr
	for c := range galaparser.NativeKids(n, native.GalaKIND_expression) {
		out = append(out, patExpr{exprSrc: exprSrc{node: c}})
	}
	return out
}

// antlrExprs is ctxs as pattern expressions.
func antlrExprs(ctxs []grammar.IExpressionContext) []patExpr {
	out := make([]patExpr, len(ctxs))
	for i, c := range ctxs {
		out[i] = antlrPatExpr(c)
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
// (lowerSuffixes), is its context: the key of the hint for the slot its value
// fills, on either tree (see expectedArgTypeStack.pushFor).
type postfixSrc struct {
	ctx   *grammar.PostfixExprContext
	node  native.GalaNodeRef
	owner *grammar.PostfixExprContext
}

// ok reports whether p is a postfix expression.
func (p postfixSrc) ok() bool { return p.ctx != nil || p.node.Ok() }

// suffixes is p's postfix suffixes, in order.
func (p postfixSrc) suffixes() []suffixSrc { return p.appendSuffixes(nil) }

// appendSuffixes appends p's postfix suffixes, in order, to out.
func (p postfixSrc) appendSuffixes(out []suffixSrc) []suffixSrc {
	if p.node.Ok() {
		for s := range galaparser.NativeKids(p.node, native.GalaKIND_postfixSuffix) {
			out = append(out, suffixSrc{node: s})
		}
		return out
	}
	for _, s := range p.ctx.AllPostfixSuffix() {
		out = append(out, suffixSrc{ctx: s.(*grammar.PostfixSuffixContext)})
	}
	return out
}

// hasSuffixes reports whether p has a postfix suffix.
func (p postfixSrc) hasSuffixes() bool {
	if p.node.Ok() {
		_, ok := firstNativeKid(p.node, native.GalaKIND_postfixSuffix)
		return ok
	}
	return p.ctx.PostfixSuffix(0) != nil
}

// hasCases reports whether p ends in a match.
func (p postfixSrc) hasCases() bool {
	if p.node.Ok() {
		_, ok := firstNativeKid(p.node, native.GalaKIND_caseClause)
		return ok
	}
	return p.ctx.CaseClause(0) != nil
}

// primaryExprNode is p's primary expression node.
func (p postfixSrc) primaryExprNode() native.PrimaryExprNode {
	return (native.PostfixExprNode{GalaNodeRef: p.node}).PrimaryExpr()
}

// hasPrimaryExpr reports whether p has a primary expression.
func (p postfixSrc) hasPrimaryExpr() bool {
	if p.node.Ok() {
		return p.primaryExprNode().Ok()
	}
	return p.ctx.PrimaryExpr() != nil
}

// primaryExpr is p's primary expression, for the expression lowering.
func (p postfixSrc) primaryExpr(t *galaASTTransformer) *grammar.PrimaryExprContext {
	if p.node.Ok() {
		return crossTo[*grammar.PrimaryExprContext](t, p.primaryExprNode().GalaNodeRef)
	}
	return p.ctx.PrimaryExpr().(*grammar.PrimaryExprContext)
}

// primaryIdent is the identifier p starts from (PrimaryOf), "" when it starts
// from anything else.
func (p postfixSrc) primaryIdent() string {
	if p.node.Ok() {
		if id := p.primaryExprNode().Primary().Identifier(); id.Ok() {
			return galaparser.NativeText(id.GalaNodeRef)
		}
		return ""
	}
	if prim := PrimaryOf(p.ctx); prim != nil && prim.Identifier() != nil {
		return prim.Identifier().GetText()
	}
	return ""
}

// tupleListNode is the parenthesized list p's native primary is, or no node.
func (p postfixSrc) tupleListNode() native.GalaNodeRef {
	return p.primaryExprNode().Primary().TupleExpressionList().GalaNodeRef
}

// suffixSrc is a postfix suffix: `.name`, `(args)` or `[exprs]`.
type suffixSrc struct {
	ctx  *grammar.PostfixSuffixContext
	node native.GalaNodeRef
}

// childCount is the number of s's children, tokens included.
func (s suffixSrc) childCount() int {
	if s.node.Ok() {
		return s.node.ChildCount()
	}
	return s.ctx.GetChildCount()
}

// opener is the token s starts with: ".", "(" or "[" (suffixOpener).
func (s suffixSrc) opener() string {
	if s.node.Ok() {
		if s.node.ChildCount() == 0 {
			return ""
		}
		if tok := s.node.Child(0).Terminal(); tok.Ok() {
			return tok.GetText()
		}
		return s.node.Child(0).Node().GetText()
	}
	return suffixOpener(s.ctx)
}

// ident is the member s selects, "" when it is no selector.
func (s suffixSrc) ident() string {
	name, _, _ := s.member()
	return name
}

// args is the arguments of s, a call suffix.
func (s suffixSrc) args() argsSrc {
	if s.node.Ok() {
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
	list, ok := s.ctx.ArgumentList().(*grammar.ArgumentListContext)
	if !ok || list == nil {
		return argsSrc{}
	}
	return antlrArgs(list)
}

// exprList is the expression list of s, a type-argument suffix, nil when s
// is none or has none.
func (s suffixSrc) exprList(t *galaASTTransformer) *grammar.ExpressionListContext {
	switch {
	case s.node.Ok():
		if el := (native.PostfixSuffixNode{GalaNodeRef: s.node}).ExpressionList(); el.Ok() {
			return crossTo[*grammar.ExpressionListContext](t, el.GalaNodeRef)
		}
	case s.ctx != nil:
		if el, ok := s.ctx.ExpressionList().(*grammar.ExpressionListContext); ok {
			return el
		}
	}
	return nil
}

// argsSrc is the arguments of a call, or the sub-patterns of a call-shaped
// pattern, and at, their argument list (the zero srcNode when the call has
// none).
type argsSrc struct {
	list []argSrc
	at   srcNode
}

// antlrArgs is the arguments of list, on ANTLR's tree.
func antlrArgs(list *grammar.ArgumentListContext) argsSrc {
	out := argsSrc{at: srcNode{ctx: list}}
	for _, a := range list.AllArgument() {
		out.list = append(out.list, argSrc{ctx: a.(*grammar.ArgumentContext)})
	}
	return out
}

// argSrc is an argument of a call, or of a call-shaped pattern: a value or a
// sub-pattern, possibly named (`Field = p`), or a lambda. The zero argSrc is
// a field named sub-patterns leave out: it has no name and no pattern.
type argSrc struct {
	ctx  *grammar.ArgumentContext
	node native.GalaNodeRef
}

// at is where a is written.
func (a argSrc) at() srcNode {
	if a.node.Ok() {
		return srcNode{node: a.node}
	}
	return srcNode{ctx: a.ctx}
}

// name is the field a names and where the name is written, "" when a is
// positional.
func (a argSrc) name() (string, srcNode) {
	switch {
	case a.node.Ok():
		if id := (native.ArgumentNode{GalaNodeRef: a.node}).Identifier(); id.Ok() {
			return galaparser.NativeText(id.GalaNodeRef), srcNode{node: id.GalaNodeRef}
		}
	case a.ctx != nil:
		if id := a.ctx.Identifier(); id != nil {
			return id.GetText(), srcNode{ctx: id}
		}
	}
	return "", srcNode{}
}

// pattern is a's sub-pattern, the zero patSrc when a is a lambda or left out.
func (a argSrc) pattern() patSrc {
	switch {
	case a.node.Ok():
		return nativePat((native.ArgumentNode{GalaNodeRef: a.node}).Pattern().GalaNodeRef)
	case a.ctx != nil:
		return antlrPat(a.ctx.Pattern())
	}
	return patSrc{}
}
