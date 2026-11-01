package transformer

import (
	"fmt"
	"go/ast"

	"github.com/antlr4-go/antlr/v4"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// Expressions on galop's native tree. An expression of the file the native
// walk reads is lowered on its native node wherever its lowering is entered:
// from a native statement (an exprSrc), and from code that still reads
// ANTLR's tree, through transformExpression and transformIfExpressionAgainst,
// which find the context's node (nativeNodeOf). That lookup is the bridge
// for callers not ported yet, and an exprSrc is the interface ported callers
// use: the lookup goes once its callers take exprSrc values. The walk below
// covers the operator chain (or, and, equality, relational, additive,
// multiplicative), unary operators, identifiers, literals, string
// interpolation, parenthesized expressions and tuples, if-expressions, and
// member accesses, indexes and calls (postfix_source.go, calls_source.go),
// with the values their arguments pass and the shape a slot-typed value is
// lowered by (args_source.go), and hands a lambda to lowerLambda on its node
// (lambdas_source.go), a partial function to transformPartialFunctionLiteral
// (partialSrc) and a composite literal to transformCompositeLiteral
// (compositeSrc). What it does not cover yet it hands to ANTLR's tree with
// crossTo: a postfix expression with a match. The blocks of an
// if-expression's branches are lowered on the native tree
// (lowerIfExprBranchBlock). Each lowering below takes the value it reads and
// is shared with the ANTLR walk, so the two give the same Go.
//
// The native readers dispatch with Kind() and read children through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// exprSrc is an expression: a context on ANTLR's tree, or a node on the
// native one.
type exprSrc struct {
	ctx  grammar.IExpressionContext
	node native.GalaNodeRef
}

// antlrExpr is the expression ctx, on ANTLR's tree.
func antlrExpr(ctx grammar.IExpressionContext) exprSrc { return exprSrc{ctx: ctx} }

// nativeExpr is the expression node n, which its reader requires: on a
// bridged tree a required child is always there, so a missing one is a bug
// in the walk or the bridge, raised as an internal error, as crossTo raises
// it, not lowered silently to no expression.
func nativeExpr(n native.GalaNodeRef) exprSrc {
	if !n.Ok() {
		// invariant: native-bridge — the walk reads only bridged trees.
		panic("native tree: no node, not an expression")
	}
	return exprSrc{node: n}
}

// onNative is e on the native tree when the walk reads it (exprOf): an
// expression of a block a caller still reads on ANTLR's tree is lowered on
// its node.
func (t *galaASTTransformer) onNative(e exprSrc) exprSrc {
	if e.node.Ok() || e.ctx == nil {
		return e
	}
	return t.exprOf(e.ctx)
}

// ok reports whether e is an expression, not the zero exprSrc.
func (e exprSrc) ok() bool { return e.node.Ok() || e.ctx != nil }

// at is where e is written.
func (e exprSrc) at() srcNode {
	if e.node.Ok() {
		return srcNode{node: e.node}
	}
	return srcNode{ctx: e.ctx}
}

// text is GetText of e.
func (e exprSrc) text() string { return e.at().text() }

// antlr is e's context: where a lowering that reads ANTLR's tree takes it
// over. nil for the zero exprSrc.
func (e exprSrc) antlr(t *galaASTTransformer) grammar.IExpressionContext {
	if e.node.Ok() {
		return crossTo[*grammar.ExpressionContext](t, e.node)
	}
	return e.ctx
}

// soleToken is the text and position of e's one token, and false when e
// spans more or none. It runs on every expression statement (loop control,
// a Go-only keyword), so it looks at the tokens rather than walking the
// precedence chain: a bare name is a single token.
func (e exprSrc) soleToken() (text string, line, col int, ok bool) {
	if e.node.Ok() {
		i := e.node.StartIndex()
		if e.node.StopIndex() != i {
			return "", 0, 0, false
		}
		tok := e.node.Start()
		return galaparser.NativeText(e.node), int(tok.Line), int(tok.Column), true
	}
	if e.ctx == nil {
		return "", 0, 0, false
	}
	start, stop := e.ctx.GetStart(), e.ctx.GetStop()
	if start == nil || stop == nil || start.GetTokenIndex() != stop.GetTokenIndex() {
		return "", 0, 0, false
	}
	return start.GetText(), start.GetLine(), start.GetColumn(), true
}

// bareIdent is the name e is, with no operator, suffix or call around it (a
// `match` may follow it), and "" when e is something else.
func (e exprSrc) bareIdent() string {
	p := e.solePostfix()
	if !p.ok() || p.hasSuffixes() {
		return ""
	}
	return p.primaryIdent()
}

// isNil reports whether e is the literal `nil` (isNilLiteral): a single
// token, checked without rendering the expression's text.
func (e exprSrc) isNil() bool {
	text, _, _, ok := e.soleToken()
	return ok && text == "nil"
}

// lowerExpr lowers e, on the tree it is on.
func (t *galaASTTransformer) lowerExpr(e exprSrc) (ast.Expr, error) {
	if e.node.Ok() {
		return t.lowerExprNative(e.node)
	}
	return t.transformExpression(e.ctx)
}

// ctxAt is n's context on ANTLR's tree, for a check that reads it: the
// crossing from a native node (crossTo). Diagnostics take it on their error
// paths only.
func (t *galaASTTransformer) ctxAt(n srcNode) antlr.ParserRuleContext {
	if n.node.Ok() {
		return crossTo[antlr.ParserRuleContext](t, n.node)
	}
	return n.ctx
}

// trackNativePosition is trackPosition of n, a native node: where an error
// raised by a deeply nested helper is reported.
func (t *galaASTTransformer) trackNativePosition(n native.GalaNodeRef) {
	start := n.Start()
	t.lastLine, t.lastCol = int(start.Line), int(start.Column)
}

// lowerExprNative is transformExpression of n, an expression node.
func (t *galaASTTransformer) lowerExprNative(n native.GalaNodeRef) (ast.Expr, error) {
	t.trackNativePosition(n)
	if t.onNativeExpr != nil {
		t.onNativeExpr(n.Index(), t.lastLine)
	}
	or, ok := firstKidNode(n)
	if !ok || or.Kind() != native.GalaKIND_orExpr {
		return nil, semanticErrorAtStart(srcNode{node: n}, "expression must contain orExpr")
	}
	return t.lowerOperatorsNative(or)
}

// nativeShape tells the onNativeShape hook, when set, that the native walk
// lowered n, an expression of shape (an operator, a name, a literal, ...).
// A lowering shared with the ANTLR walk calls it with no node there, and it
// reports nothing.
func (t *galaASTTransformer) nativeShape(n native.GalaNodeRef, shape string) {
	if t.onNativeShape != nil && n.Ok() {
		t.onNativeShape(int(n.Start().Line), shape)
	}
}

// operandKind is the kind of the operands of a binary-operator level, and
// the names its diagnostic gives the two; false when level is no such level.
func operandKind(level native.GalaKind) (native.GalaKind, string, string, bool) {
	switch level {
	case native.GalaKIND_orExpr:
		return native.GalaKIND_andExpr, "orExpr", "andExpr", true
	case native.GalaKIND_andExpr:
		return native.GalaKIND_equalityExpr, "andExpr", "equalityExpr", true
	case native.GalaKIND_equalityExpr:
		return native.GalaKIND_relationalExpr, "equalityExpr", "relationalExpr", true
	case native.GalaKIND_relationalExpr:
		return native.GalaKIND_additiveExpr, "relationalExpr", "additiveExpr", true
	case native.GalaKIND_additiveExpr:
		return native.GalaKIND_multiplicativeExpr, "additiveExpr", "multiplicativeExpr", true
	case native.GalaKIND_multiplicativeExpr:
		return native.GalaKIND_unaryExpr, "multiplicativeExpr", "unaryExpr", true
	}
	return 0, "", "", false
}

// lowerOperatorsNative lowers n, a level of the binary-operator chain (an
// orExpr down to a multiplicativeExpr) or a unaryExpr: its operands, left
// to right, each joined to the ones before it by the operator written
// between them, as transformOrExpr and the levels below it do.
func (t *galaASTTransformer) lowerOperatorsNative(n native.GalaNodeRef) (ast.Expr, error) {
	level := n.Kind()
	if level == native.GalaKIND_unaryExpr {
		return t.lowerUnaryNative(native.UnaryExprNode{GalaNodeRef: n})
	}
	kind, name, operandName, ok := operandKind(level)
	if !ok {
		return nil, semanticErrorAtStart(srcNode{node: n}, fmt.Sprintf("internal error: node of kind %d is no operator level", level))
	}
	var (
		result ast.Expr
		prev   native.GalaNodeRef
		op     string
	)
	for i := range n.ChildCount() {
		c := n.Child(i)
		if c.IsToken() {
			op = c.Terminal().GetText()
			continue
		}
		operand := c.Node()
		if operand.Kind() != kind {
			return nil, semanticErrorAtStart(srcNode{node: operand}, fmt.Sprintf("internal error: %s has an operand of kind %d, not %s", name, operand.Kind(), operandName))
		}
		right, err := t.lowerOperatorsNative(operand)
		if err != nil {
			return nil, err
		}
		if !prev.Ok() {
			result, prev = right, operand
			continue
		}
		t.nativeShape(n, "operator")
		if result, err = t.binaryOperation(op, t.getBinaryToken(op), srcNode{node: n}, result, right); err != nil {
			return nil, err
		}
		if level == native.GalaKIND_equalityExpr {
			if result, err = t.lowerEquality(srcNode{node: prev}, srcNode{node: operand}, result); err != nil {
				return nil, err
			}
		}
		prev = operand
	}
	if !prev.Ok() {
		return nil, semanticErrorAtStart(srcNode{node: n}, fmt.Sprintf("%s must have at least one %s", name, operandName))
	}
	return result, nil
}

// lowerUnaryNative is transformUnaryExpr of u.
func (t *galaASTTransformer) lowerUnaryNative(u native.UnaryExprNode) (ast.Expr, error) {
	if op := u.UnaryOp(); op.Ok() {
		t.nativeShape(u.GalaNodeRef, "unary")
		inner := u.UnaryExpr()
		expr, err := t.lowerUnaryNative(inner)
		if err != nil {
			return nil, err
		}
		return t.unaryOperation(galaparser.NativeText(op.GalaNodeRef), srcNode{node: u.GalaNodeRef}, srcNode{node: inner.GalaNodeRef}, expr)
	}
	if p := u.PostfixExpr(); p.Ok() {
		return t.lowerPostfixNative(p)
	}
	return nil, semanticErrorAtStart(srcNode{node: u.GalaNodeRef}, "unaryExpr must have unaryOp or postfixExpr")
}

// lowerPostfixNative is transformPostfixExpr of p (see postfix_source.go).
func (t *galaASTTransformer) lowerPostfixNative(p native.PostfixExprNode) (ast.Expr, error) {
	prim, ok := firstKidNode(p.GalaNodeRef)
	if !ok || prim.Kind() != native.GalaKIND_primaryExpr {
		return nil, semanticErrorAtStart(srcNode{node: p.GalaNodeRef}, "postfixExpr must have primaryExpr")
	}
	if p.ChildCount() == 1 {
		return t.lowerPrimaryExprNative(prim)
	}
	return t.lowerSuffixedNative(p)
}

// lowerPrimaryExprNative is transformPrimaryExpr of n, a primaryExpr node.
func (t *galaASTTransformer) lowerPrimaryExprNative(n native.GalaNodeRef) (ast.Expr, error) {
	if kid, ok := firstKidNode(n); ok {
		switch kid.Kind() {
		case native.GalaKIND_primary:
			return t.lowerPrimaryNative(native.PrimaryNode{GalaNodeRef: kid})
		case native.GalaKIND_lambdaExpression:
			// The context-free form, as transformLambda lowers it.
			return t.lowerLambda(lambdaSrc{node: native.LambdaExpressionNode{GalaNodeRef: kid}}, nil, nil, true, false)
		case native.GalaKIND_ifExpression:
			return t.lowerIfExpression(ifExprSrc{node: kid}, slot{})
		case native.GalaKIND_partialFunctionLiteral:
			return t.transformPartialFunctionLiteral(partialSrc{node: kid}, slot{})
		}
	}
	return nil, semanticErrorAtStart(srcNode{node: n}, "primaryExpr must have primary, lambda, if expression, or partial function")
}

// lowerPrimaryNative is transformPrimary of p.
func (t *galaASTTransformer) lowerPrimaryNative(p native.PrimaryNode) (ast.Expr, error) {
	at := srcNode{node: p.GalaNodeRef}
	var list native.GalaNodeRef
	if kid, ok := firstKidNode(p.GalaNodeRef); ok {
		switch kid.Kind() {
		case native.GalaKIND_identifier:
			t.nativeShape(kid, "name")
			return t.lowerIdentifier(galaparser.NativeText(kid), at)
		case native.GalaKIND_literal:
			t.nativeShape(kid, "literal")
			return t.lowerLiteral(nativeLiteral(native.LiteralNode{GalaNodeRef: kid}))
		case native.GalaKIND_compositeLiteral:
			return t.transformCompositeLiteral(compositeSrc{node: kid})
		case native.GalaKIND_tupleExpressionList:
			list = kid
		}
	}
	t.nativeShape(p.GalaNodeRef, "parens")
	// Most parentheses hold one expression or two: no allocation for them.
	var buf [2]exprSrc
	elems := buf[:0]
	for e := range galaparser.NativeKids(list, native.GalaKIND_expression) {
		elems = append(elems, nativeExpr(e))
	}
	return t.lowerParenthesized(elems, at)
}

// litKind is what a literal is.
type litKind uint8

const (
	litNone litKind = iota
	litInt
	litFloat
	litString
	litChar
	litRawString
	litInterpolated
	litFormat
	litWord // true, false or nil
)

// litTok is a literal's token: its text and where it starts.
type litTok struct {
	text      string
	line, col int
}

// literalSrc is a literal, as its lowering reads it.
type literalSrc struct {
	kind litKind
	tok  litTok
}

// antlrLitTok is node's litTok.
func antlrLitTok(node antlr.TerminalNode) litTok {
	sym := node.GetSymbol()
	return litTok{text: node.GetText(), line: sym.GetLine(), col: sym.GetColumn()}
}

// antlrLiteral reads the literal ctx on ANTLR's tree.
func antlrLiteral(ctx *grammar.LiteralContext) literalSrc {
	// The first accessor that finds its token wins, as transformLiteral's
	// checks did.
	for _, a := range [...]struct {
		token func() antlr.TerminalNode
		kind  litKind
	}{
		{ctx.INT_LIT, litInt},
		{ctx.FLOAT_LIT, litFloat},
		{ctx.STRING, litString},
		{ctx.CHAR_LIT, litChar},
		{ctx.RAW_STRING, litRawString},
		{ctx.INTERPOLATED_STRING, litInterpolated},
		{ctx.FORMAT_STRING, litFormat},
	} {
		if node := a.token(); node != nil {
			return literalSrc{kind: a.kind, tok: antlrLitTok(node)}
		}
	}
	return wordLiteral(ctx.GetText())
}

// nativeLiteral is antlrLiteral of l.
func nativeLiteral(l native.LiteralNode) literalSrc {
	if l.ChildCount() == 1 {
		if c := l.Child(0); c.IsToken() {
			term := c.Terminal()
			tok := term.Token()
			lt := litTok{text: term.GetText(), line: int(tok.Line), col: int(tok.Column)}
			switch tok.Type {
			case native.GalaINT_LIT:
				return literalSrc{kind: litInt, tok: lt}
			case native.GalaFLOAT_LIT:
				return literalSrc{kind: litFloat, tok: lt}
			case native.GalaSTRING:
				return literalSrc{kind: litString, tok: lt}
			case native.GalaCHAR_LIT:
				return literalSrc{kind: litChar, tok: lt}
			case native.GalaRAW_STRING:
				return literalSrc{kind: litRawString, tok: lt}
			case native.GalaINTERPOLATED_STRING:
				return literalSrc{kind: litInterpolated, tok: lt}
			case native.GalaFORMAT_STRING:
				return literalSrc{kind: litFormat, tok: lt}
			}
			return wordLiteral(lt.text)
		}
	}
	return literalSrc{}
}

// wordLiteral is the literal `true`, `false` or `nil` written as text; no
// literal for any other text. Its lowering reads only the text.
func wordLiteral(text string) literalSrc {
	switch text {
	case "true", "false", "nil":
		return literalSrc{kind: litWord, tok: litTok{text: text}}
	}
	return literalSrc{}
}

// ifExprSrc is an if-expression.
type ifExprSrc struct {
	ctx  *grammar.IfExpressionContext
	node native.GalaNodeRef
}

// ifExprOf is the if-expression ctx, on the native tree when the walk reads
// it (nativeNodeOf).
func (t *galaASTTransformer) ifExprOf(ctx *grammar.IfExpressionContext) ifExprSrc {
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_ifExpression); ok {
		return ifExprSrc{node: n}
	}
	return ifExprSrc{ctx: ctx}
}

// at is where e is written.
func (e ifExprSrc) at() srcNode {
	if e.node.Ok() {
		return srcNode{node: e.node}
	}
	return srcNode{ctx: e.ctx}
}

// cond is e's condition.
func (e ifExprSrc) cond() exprSrc {
	if e.node.Ok() {
		return nativeExpr(native.IfExpressionNode{GalaNodeRef: e.node}.Expression().GalaNodeRef)
	}
	return antlrExpr(e.ctx.Expression())
}

// branches is e's then and else branches.
func (e ifExprSrc) branches() [2]ifBranchSrc {
	var out [2]ifBranchSrc
	if e.node.Ok() {
		i := 0
		for b := range galaparser.NativeKids(e.node, native.GalaKIND_ifExprBranch) {
			if i == len(out) {
				break
			}
			out[i] = ifBranchSrc{node: b}
			i++
		}
		return out
	}
	for i, b := range e.ctx.AllIfExprBranch() {
		if i == len(out) {
			break
		}
		out[i] = ifBranchSrc{ctx: b.(*grammar.IfExprBranchContext)}
	}
	return out
}

// branchesEscape is escapesConstruct of branches, on their tree.
func (t *galaASTTransformer) branchesEscape(branches [2]ifBranchSrc) bool {
	for _, b := range branches {
		if (b.node.Ok() || b.ctx != nil) && t.escapesAt(b.at()) {
			return true
		}
	}
	return false
}

// ifBranchSrc is a branch of an if-expression: an expression or a block.
type ifBranchSrc struct {
	ctx  *grammar.IfExprBranchContext
	node native.GalaNodeRef
}

// ifBranchOf is the if-expression branch ctx, on the native tree when the
// walk reads it (nativeNodeOf), for the callers that still read ANTLR's tree.
func (t *galaASTTransformer) ifBranchOf(ctx *grammar.IfExprBranchContext) ifBranchSrc {
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_ifExprBranch); ok {
		return ifBranchSrc{node: n}
	}
	return ifBranchSrc{ctx: ctx}
}

// ok reports whether b is a branch, not the zero ifBranchSrc.
func (b ifBranchSrc) ok() bool { return b.ctx != nil || b.node.Ok() }

// block is b's block, and the zero blockSrc when b is an expression.
func (b ifBranchSrc) block() blockSrc {
	if b.node.Ok() {
		if blk := (native.IfExprBranchNode{GalaNodeRef: b.node}).Block(); blk.Ok() {
			return blockSrc{node: blk.GalaNodeRef}
		}
		return blockSrc{}
	}
	if blk, ok := b.ctx.Block().(*grammar.BlockContext); ok && blk != nil {
		return blockSrc{ctx: blk}
	}
	return blockSrc{}
}

// at is where b is written.
func (b ifBranchSrc) at() srcNode {
	if b.node.Ok() {
		return srcNode{node: b.node}
	}
	return srcNode{ctx: b.ctx}
}

// expr is b's expression, and the zero exprSrc when b is a block.
func (b ifBranchSrc) expr() exprSrc {
	if b.node.Ok() {
		if e := (native.IfExprBranchNode{GalaNodeRef: b.node}).Expression(); e.Ok() {
			return nativeExpr(e.GalaNodeRef)
		}
		return exprSrc{}
	}
	if e := b.ctx.Expression(); e != nil {
		return antlrExpr(e)
	}
	return exprSrc{}
}

// lowerIfExprBranch lowers b, an if-expression branch, an expression or a
// block, against s, the if-expression's slot (see lowerAgainst).
func (t *galaASTTransformer) lowerIfExprBranch(b ifBranchSrc, s slot) (loweredIfBranch, error) {
	if e := b.expr(); e.ok() {
		return t.lowerIfExprBranchValue(e, s)
	}
	return t.lowerIfExprBranchBlock(b.block(), s)
}
