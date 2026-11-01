package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"slices"
)

// NOTE: transformCallExpr was removed - it was dead code.
// Call transformation goes through transformCallWithArgsCtx.

func (t *galaASTTransformer) transformExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}
	// An expression of the file the native walk reads is lowered on its
	// native node, wherever its lowering is entered from (see
	// expressions_source.go).
	if n, ok := t.nativeNodeOf(ctx, native.GalaKIND_expression); ok {
		return t.lowerExprNative(n)
	}
	// Track position for error reporting in deeply-nested helpers
	if prc, ok := ctx.(antlr.ParserRuleContext); ok {
		t.trackPosition(prc)
	}

	// With the new grammar, expression simply wraps orExpr
	if orExpr := ctx.OrExpr(); orExpr != nil {
		return t.transformOrExpr(orExpr.(*grammar.OrExprContext))
	}

	return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "expression must contain orExpr")
}

func (t *galaASTTransformer) transformOrExpr(ctx *grammar.OrExprContext) (ast.Expr, error) {
	andExprs := ctx.AllAndExpr()
	if len(andExprs) == 0 {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "orExpr must have at least one andExpr")
	}

	result, err := t.transformAndExpr(andExprs[0].(*grammar.AndExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(andExprs); i++ {
		right, err := t.transformAndExpr(andExprs[i].(*grammar.AndExprContext))
		if err != nil {
			return nil, err
		}
		if result, err = t.binaryOperation("||", token.LOR, srcNode{ctx: ctx}, result, right); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformAndExpr(ctx *grammar.AndExprContext) (ast.Expr, error) {
	eqExprs := ctx.AllEqualityExpr()
	if len(eqExprs) == 0 {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "andExpr must have at least one equalityExpr")
	}

	result, err := t.transformEqualityExpr(eqExprs[0].(*grammar.EqualityExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(eqExprs); i++ {
		right, err := t.transformEqualityExpr(eqExprs[i].(*grammar.EqualityExprContext))
		if err != nil {
			return nil, err
		}
		if result, err = t.binaryOperation("&&", token.LAND, srcNode{ctx: ctx}, result, right); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformEqualityExpr(ctx *grammar.EqualityExprContext) (ast.Expr, error) {
	relExprs := ctx.AllRelationalExpr()
	if len(relExprs) == 0 {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "equalityExpr must have at least one relationalExpr")
	}

	result, err := t.transformRelationalExpr(relExprs[0].(*grammar.RelationalExprContext))
	if err != nil {
		return nil, err
	}

	// Get the operators between expressions
	for i := 1; i < len(relExprs); i++ {
		// The operator is at position (i*2 - 1) in children
		opText, err := getChildOperatorText(ctx, i*2-1)
		if err != nil {
			return nil, err
		}
		right, err := t.transformRelationalExpr(relExprs[i].(*grammar.RelationalExprContext))
		if err != nil {
			return nil, err
		}
		tok := t.getBinaryToken(opText)
		if result, err = t.binaryOperation(opText, tok, srcNode{ctx: ctx}, result, right); err != nil {
			return nil, err
		}
		if result, err = t.lowerEquality(srcNode{ctx: relExprs[i-1]}, srcNode{ctx: relExprs[i]}, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformRelationalExpr(ctx *grammar.RelationalExprContext) (ast.Expr, error) {
	addExprs := ctx.AllAdditiveExpr()
	if len(addExprs) == 0 {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "relationalExpr must have at least one additiveExpr")
	}

	result, err := t.transformAdditiveExpr(addExprs[0].(*grammar.AdditiveExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(addExprs); i++ {
		opText, err := getChildOperatorText(ctx, i*2-1)
		if err != nil {
			return nil, err
		}
		right, err := t.transformAdditiveExpr(addExprs[i].(*grammar.AdditiveExprContext))
		if err != nil {
			return nil, err
		}
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), srcNode{ctx: ctx}, result, right); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformAdditiveExpr(ctx *grammar.AdditiveExprContext) (ast.Expr, error) {
	mulExprs := ctx.AllMultiplicativeExpr()
	if len(mulExprs) == 0 {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "additiveExpr must have at least one multiplicativeExpr")
	}

	result, err := t.transformMultiplicativeExpr(mulExprs[0].(*grammar.MultiplicativeExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(mulExprs); i++ {
		opText, err := getChildOperatorText(ctx, i*2-1)
		if err != nil {
			return nil, err
		}
		right, err := t.transformMultiplicativeExpr(mulExprs[i].(*grammar.MultiplicativeExprContext))
		if err != nil {
			return nil, err
		}
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), srcNode{ctx: ctx}, result, right); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformMultiplicativeExpr(ctx *grammar.MultiplicativeExprContext) (ast.Expr, error) {
	unaryExprs := ctx.AllUnaryExpr()
	if len(unaryExprs) == 0 {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "multiplicativeExpr must have at least one unaryExpr")
	}

	result, err := t.transformUnaryExpr(unaryExprs[0].(*grammar.UnaryExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(unaryExprs); i++ {
		opText, err := getChildOperatorText(ctx, i*2-1)
		if err != nil {
			return nil, err
		}
		right, err := t.transformUnaryExpr(unaryExprs[i].(*grammar.UnaryExprContext))
		if err != nil {
			return nil, err
		}
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), srcNode{ctx: ctx}, result, right); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformUnaryExpr(ctx *grammar.UnaryExprContext) (ast.Expr, error) {
	// Check for unary operator
	if unaryOp := ctx.UnaryOp(); unaryOp != nil {
		innerUnary := ctx.UnaryExpr().(*grammar.UnaryExprContext)
		expr, err := t.transformUnaryExpr(innerUnary)
		if err != nil {
			return nil, err
		}
		return t.unaryOperation(unaryOp.GetText(), srcNode{ctx: ctx}, srcNode{ctx: innerUnary}, expr)
	}

	// Otherwise it's a postfixExpr
	if postfix := ctx.PostfixExpr(); postfix != nil {
		return t.transformPostfixExpr(postfix.(*grammar.PostfixExprContext))
	}

	return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "unaryExpr must have unaryOp or postfixExpr")
}

// unaryOperation builds `op expr` from the lowered operand expr of the unary
// expression at, whose operand is written at operand. Taking the address of a
// val is GALA-E0058 (immutableAddressError, which reads the operand's
// ANTLR text only for its diagnostic).
func (t *galaASTTransformer) unaryOperation(op string, at, operand srcNode, expr ast.Expr) (ast.Expr, error) {
	switch op {
	case "&":
		if err := t.immutableAddressError(at, operand, expr); err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Op: token.AND, X: t.sealPlainValPath(expr)}, nil
	case "*":
		return &ast.StarExpr{X: expr}, nil
	case "!":
		expr = t.wrapWithAssertion(expr, ast.NewIdent("bool"))
	}
	// Automatic unwrapping for other unary operands
	expr = t.unwrapImmutable(expr)
	return &ast.UnaryExpr{Op: t.getUnaryToken(op), X: expr}, nil
}

// getChildOperatorText safely extracts the operator text from a parse tree child node.
func getChildOperatorText(ctx antlr.ParserRuleContext, index int) (string, error) {
	child := ctx.GetChild(index)
	if child == nil {
		return "", galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "missing operator in expression")
	}
	tree, ok := child.(antlr.ParseTree)
	if !ok {
		return "", galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "unexpected operator node in expression")
	}
	return tree.GetText(), nil
}

// binaryOperation builds `left op right` from two lowered operands: it rejects
// a Go call's Try or Tuple as an operand (GALA-E0049) and reads vals through
// their Immutable wrapper.
func (t *galaASTTransformer) binaryOperation(op string, tok token.Token, at srcNode, left, right ast.Expr) (ast.Expr, error) {
	if err := t.checkGoResultOperands(op, at, left, right); err != nil {
		return nil, err
	}
	if err := t.checkPtrNilComparison(tok, at, left, right); err != nil {
		return nil, err
	}
	return &ast.BinaryExpr{X: t.unwrapImmutable(left), Op: tok, Y: t.unwrapImmutable(right)}, nil
}

func (t *galaASTTransformer) isBinaryOperator(op string) bool {
	switch op {
	case "||", "&&", "==", "!=", "<", "<=", ">", ">=",
		"+", "-", "|", "^", "*", "/", "%", "<<", ">>", "&", "&^":
		return true
	default:
		return false
	}
}

// LeadingPostfixExpr walks the precedence chain (or → and → equality →
// relational → additive → multiplicative → unary) down to the postfix
// expression of ctx's first operand, or nil when a unary operator or no
// operand intervenes. With sole set, every level must hold exactly one
// operand, i.e. ctx has no binary operator at all.
func LeadingPostfixExpr(ctx grammar.IExpressionContext, sole bool) *grammar.PostfixExprContext {
	addExpr := leadingAdditiveExpr(ctx, sole)
	if addExpr == nil {
		return nil
	}
	fits := func(n int) bool { return n > 0 && (!sole || n == 1) }
	mulExprs := addExpr.AllMultiplicativeExpr()
	if !fits(len(mulExprs)) {
		return nil
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if !fits(len(unaryExprs)) {
		return nil
	}
	postfix, _ := unaryExprs[0].(*grammar.UnaryExprContext).PostfixExpr().(*grammar.PostfixExprContext)
	return postfix
}

// leadingAdditiveExpr walks the precedence chain of ctx down to the additive
// expression of its first operand, as LeadingPostfixExpr does. With sole set,
// no `||`, `&&` or comparison may join several operands above it.
func leadingAdditiveExpr(ctx grammar.IExpressionContext, sole bool) *grammar.AdditiveExprContext {
	if ctx == nil {
		return nil
	}
	fits := func(n int) bool { return n > 0 && (!sole || n == 1) }
	orExpr, _ := ctx.OrExpr().(*grammar.OrExprContext)
	if orExpr == nil {
		return nil
	}
	andExprs := orExpr.AllAndExpr()
	if !fits(len(andExprs)) {
		return nil
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if !fits(len(eqExprs)) {
		return nil
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if !fits(len(relExprs)) {
		return nil
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if !fits(len(addExprs)) {
		return nil
	}
	addExpr, _ := addExprs[0].(*grammar.AdditiveExprContext)
	return addExpr
}

// PrimaryOf returns the primary a postfix expression starts from, or nil when
// it starts from a lambda, if-expression or partial function instead.
func PrimaryOf(postfix *grammar.PostfixExprContext) *grammar.PrimaryContext {
	primaryExpr := postfixSrc{ctx: postfix}.antlrPrimaryExpr()
	if primaryExpr == nil {
		return nil
	}
	primary, _ := primaryExpr.Primary().(*grammar.PrimaryContext)
	return primary
}

// getCallPatternFromExpression checks if an expression is a call pattern like Left(n)
// and returns the base expression context and argument list.
// Returns nil values if not a call pattern.
func (t *galaASTTransformer) getCallPatternFromExpression(ctx grammar.IExpressionContext) (*grammar.PrimaryExprContext, *grammar.ArgumentListContext) {
	primaryExpr, argList, _ := t.getCallPatternWithTypeArgsFromExpression(ctx)
	return primaryExpr, argList
}

// getSinglePostfixExpr unwraps an expression that is a single operand with no
// binary or unary operators down to its PostfixExprContext, navigating
// expression -> orExpr -> andExpr -> ... -> unaryExpr -> postfixExpr. It returns
// nil for any compound (binary/unary-prefixed) expression. This is the shared
// front half of the call-pattern detectors, which only apply to an atomic
// postfix expression like `Foo(x)`, `Foo[T](x)`, or `pkg.Foo(x)`.
func (t *galaASTTransformer) getSinglePostfixExpr(ctx grammar.IExpressionContext) *grammar.PostfixExprContext {
	if ctx == nil {
		return nil
	}
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return nil
	}
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) != 1 {
		return nil
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) != 1 {
		return nil
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) != 1 {
		return nil
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) != 1 {
		return nil
	}
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) != 1 {
		return nil
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) != 1 {
		return nil
	}
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)
	if unaryCtx.UnaryOp() != nil {
		return nil
	}
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil
	}
	return postfixExpr.(*grammar.PostfixExprContext)
}

// getCallPatternWithTypeArgsFromExpression checks if an expression is a call pattern
// and returns the base expression context, argument list, and any explicit type arguments.
// This handles both simple patterns like Left(n) and generic patterns like Unwrap[int](v).
// Returns nil values if not a call pattern.
func (t *galaASTTransformer) getCallPatternWithTypeArgsFromExpression(ctx grammar.IExpressionContext) (*grammar.PrimaryExprContext, *grammar.ArgumentListContext, *grammar.ExpressionListContext) {
	postfixCtx := t.getSinglePostfixExpr(ctx)
	if postfixCtx == nil {
		return nil, nil, nil
	}

	suffixes := postfixCtx.AllPostfixSuffix()
	if len(suffixes) == 0 || len(suffixes) > 2 {
		return nil, nil, nil
	}

	var typeArgsSuffix *grammar.PostfixSuffixContext
	var callSuffix *grammar.PostfixSuffixContext

	if len(suffixes) == 1 {
		// Single suffix - must be a call
		callSuffix = suffixes[0].(*grammar.PostfixSuffixContext)
	} else if len(suffixes) == 2 {
		// Two suffixes - first should be type args [T], second should be call (...)
		typeArgsSuffix = suffixes[0].(*grammar.PostfixSuffixContext)
		callSuffix = suffixes[1].(*grammar.PostfixSuffixContext)

		// Verify first suffix is type args (starts with '[')
		if typeArgsSuffix.GetChildCount() < 2 {
			return nil, nil, nil
		}
		firstChild := typeArgsSuffix.GetChild(0).(antlr.ParseTree).GetText()
		if firstChild != "[" {
			return nil, nil, nil
		}
	}

	// Verify call suffix starts with '('
	if callSuffix.GetChildCount() < 2 {
		return nil, nil, nil
	}
	callFirstChild := callSuffix.GetChild(0).(antlr.ParseTree).GetText()
	if callFirstChild != "(" {
		return nil, nil, nil
	}

	// Get the primary expression
	primaryExpr := postfixCtx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, nil, nil
	}

	// Get argument list (may be nil for empty calls)
	var argList *grammar.ArgumentListContext
	if al := callSuffix.ArgumentList(); al != nil {
		argList = al.(*grammar.ArgumentListContext)
	}

	// Get explicit type arguments (may be nil if no type args)
	var typeArgs *grammar.ExpressionListContext
	if typeArgsSuffix != nil {
		if el := typeArgsSuffix.ExpressionList(); el != nil {
			typeArgs = el.(*grammar.ExpressionListContext)
		}
	}

	return primaryExpr.(*grammar.PrimaryExprContext), argList, typeArgs
}

func (t *galaASTTransformer) getBinaryToken(op string) token.Token {
	switch op {
	case "||":
		return token.LOR
	case "&&":
		return token.LAND
	case "==":
		return token.EQL
	case "!=":
		return token.NEQ
	case "<":
		return token.LSS
	case "<=":
		return token.LEQ
	case ">":
		return token.GTR
	case ">=":
		return token.GEQ
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "|":
		return token.OR
	case "^":
		return token.XOR
	case "*":
		return token.MUL
	case "/":
		return token.QUO
	case "%":
		return token.REM
	case "<<":
		return token.SHL
	case ">>":
		return token.SHR
	case "&":
		return token.AND
	case "&^":
		return token.AND_NOT
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) getUnaryToken(op string) token.Token {
	switch op {
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "!":
		return token.NOT
	case "^":
		return token.XOR
	case "&":
		return token.AND
	case "<-":
		return token.ARROW
	default:
		return token.ILLEGAL
	}
}

// transformPrimary, lowerIdentifier, lowerParenthesized, transformCompositeLiteral, lowerLiteral moved to constructors.go
// Lambda-related functions moved to lambdas.go
func (t *galaASTTransformer) transformIfExpression(ctx *grammar.IfExpressionContext) (ast.Expr, error) {
	return t.transformIfExpressionAgainst(ctx, slot{})
}

// transformIfExpressionAgainst lowers an if-expression to an IIFE. s is the slot
// it fills (zero when none): each branch value is lowered against it, and its
// type informs the IIFE's result type (see branchingResultType).
func (t *galaASTTransformer) transformIfExpressionAgainst(ctx *grammar.IfExpressionContext, s slot) (ast.Expr, error) {
	return t.lowerIfExpression(t.ifExprOf(ctx), s)
}

// lowerIfExpression is transformIfExpressionAgainst of e, on either tree.
func (t *galaASTTransformer) lowerIfExpression(e ifExprSrc, s slot) (ast.Expr, error) {
	// 'if' '(' cond ')' thenBranch 'else' elseBranch
	// Branches can be either expressions or blocks.
	if e.node.Ok() {
		t.nativeShape(e.node, "if")
	}
	cond, err := t.lowerExpr(e.cond())
	if err != nil {
		return nil, err
	}
	line, col := e.at().start()
	// The if-expression lowers to an IIFE, which gives its branches their own
	// return slot: a `return` in a branch leaves only the IIFE (GALA-E0069
	// unless the IIFE is itself the result, see branching_calls.go). An open
	// slot type holds placeholders, so it is no return type.
	branches := e.branches()
	// An if-expression whose value a local declaration stores, and whose
	// branches hold a `return`, `break` or `continue`, is lowered as
	// statements storing its value (see hoisted_value.go): its control flow
	// acts on the enclosing function or loop.
	if s.hoist != "" && !t.branchesEscape(branches) {
		s.hoist = ""
	}
	if s.hoist == "" {
		defer t.enterIIFEReturnSlot(s.closedType())()
	}

	// A branch with no slot type to lower against is typed by the other one
	// (see lowerBranches).
	var lowered [2]loweredIfBranch
	siblingTyped := transpiler.IsUnusable(s.typ)
	lowerBranch := func(i int, bs slot) (transpiler.Type, error) {
		b := &lowered[i]
		var err error
		if *b, err = t.lowerIfExprBranch(branches[i], bs); err != nil || !siblingTyped {
			return nil, err
		}
		if s.hoist != "" && (b.terminates || t.leaves(b.stmts)) {
			// The branch leaves the function or loop: it has no value.
			return nil, nil
		}
		return t.getExprTypeName(b.expr), nil
	}
	if err := t.lowerBranches(2, s, siblingTyped, lowerBranch); err != nil {
		return nil, err
	}
	if s.hoist != "" {
		return t.hoistIfExpression(e, cond, s, lowered)
	}
	if err := t.checkNoLoopControlInValue("an if-expression", lowered[0].stmts, lowered[1].stmts); err != nil {
		return nil, err
	}
	thenExpr, elseExpr := lowered[0].expr, lowered[1].expr

	// The if-expression is typed by unifying its branch types, as a
	// block-bodied lambda's result is (unifyBlockReturnTypes). A branch with
	// no value (empty, or a void call) does not take part: its type is not
	// the value's.
	branchTypes := [2]transpiler.Type{t.getExprTypeName(thenExpr), t.getExprTypeName(elseExpr)}
	unified := branchTypes
	novalue := 0
	for i, bt := range unified {
		if bt.IsVoid() || lowered[i].noValue {
			unified[i] = transpiler.NilType{}
			novalue++
		}
	}
	retType := t.unifyBranchTypes(unified[0], unified[1])
	if novalue == len(unified) {
		// No branch has a value: the if-expression is void.
		retType = transpiler.VoidType{}
	}
	// A branch typed `any` — the value of a Go API that returns it — is a
	// known type: the if-expression's value is `any` too.
	if retType.IsNil() {
		for _, bt := range branchTypes {
			if bt.IsAny() {
				retType = bt
				break
			}
		}
	}

	// Neither branch has a type, and nothing the if-expression fills gives it
	// one. When every branch produces no value (`{}`, a trailing statement or
	// a void call) it runs like the void case below; any other untyped value
	// has no type the closure could return.
	if transpiler.IsUnusable(t.branchingResultType(retType, s)) {
		for i, b := range lowered {
			if !b.noValue && !branchTypes[i].IsVoid() {
				return nil, unknownBranchTypeError(line, col)
			}
		}
		retType = transpiler.VoidType{}
	}

	// Both branches are void calls, as in the statement `if (c) a() else b()`:
	// there is no value to return, so the branches run as statements in a
	// closure with no result. Returning them would emit `func() void`, which
	// is not Go, and `return a()` of a void call, which Go rejects. Such a
	// closure can only be run as a statement (see checkBranchingCalls).
	if _, isVoid := retType.(transpiler.VoidType); isVoid {
		branchBody := func(b loweredIfBranch) *ast.BlockStmt {
			if b.needsValueReturn() && !b.noValue {
				return &ast.BlockStmt{List: append(b.stmts, &ast.ExprStmt{X: b.expr})}
			}
			return &ast.BlockStmt{List: b.stmts}
		}
		call := &ast.CallExpr{
			Fun: &ast.FuncLit{
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.IfStmt{
					Cond: cond,
					Body: branchBody(lowered[0]),
					Else: branchBody(lowered[1]),
				}}},
			},
		}
		t.recordBranchingCall(call, "if-expression", line, col)
		return call, nil
	}

	// The if-expression's value is used, so every branch must produce one.
	if err := t.checkIfBranchValues(branches, lowered, true); err != nil {
		return nil, err
	}

	retTypeExpr := t.typeToExpr(t.branchingResultType(retType, s))

	// Each block is the branch's statements, then `return <value>` unless the
	// branch already returns: it ends in an explicit return (the synthesized
	// one would be dead code returning a placeholder), or its trailing if/else
	// chain returns the value from each of its branches.
	body := func(b loweredIfBranch) *ast.BlockStmt {
		switch {
		case b.needsValueReturn() && t.isNoReturnCallExpr(b.expr):
			// A diverging call (`Panic`) has no value to return.
			return &ast.BlockStmt{List: append(b.stmts, &ast.ExprStmt{X: b.expr})}
		case b.needsValueReturn():
			return &ast.BlockStmt{List: append(b.stmts, &ast.ReturnStmt{Results: []ast.Expr{b.expr}})}
		}
		return &ast.BlockStmt{List: b.stmts}
	}

	// Transpile to IIFE: func() T { if cond { ...thenBody } else { ...elseBody } }()
	call := &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: retTypeExpr}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.IfStmt{
						Cond: cond,
						Body: body(lowered[0]),
						Else: body(lowered[1]),
					},
				},
			},
		},
	}
	t.recordBranchingCall(call, "if-expression", line, col)
	return call, nil
}

// unifyBranchTypes is the type two branches' value types unify to: the more
// specific of the two when both are known, the known one when only one is,
// NilType otherwise. A branch with no value passes NilType.
func (t *galaASTTransformer) unifyBranchTypes(thenT, elseT transpiler.Type) transpiler.Type {
	thenKnown, elseKnown := !transpiler.IsUnusableOrAny(thenT), !transpiler.IsUnusableOrAny(elseT)
	switch {
	case thenKnown && elseKnown:
		if thenT.String() == elseT.String() {
			return thenT
		}
		if unified := t.pickMoreSpecificType(thenT, elseT); unified != nil {
			return unified
		}
	case thenKnown:
		return thenT
	case elseKnown:
		return elseT
	}
	return transpiler.NilType{}
}

// loweredIfBranch is a lowered branch of an if-expression (see
// transformIfExprBranch): stmts run first, then expr is its value, unless the
// branch terminates.
type loweredIfBranch struct {
	stmts      []ast.Stmt
	expr       ast.Expr
	terminates bool
	// valueInStmts: stmts end in an if/else chain each branch of which
	// returns the value (synthesized arm returns, see armReturn); expr is one
	// of those values, for typing.
	valueInStmts bool
	// noValue: the branch produces no value — an empty block, or one ending
	// in a declaration, an assignment, a loop or an if with a branch that
	// ends in none; expr is the placeholder `nil`.
	noValue bool
}

// needsValueReturn reports whether the branch's value expr still has to be
// returned (or stored) after its statements.
func (b loweredIfBranch) needsValueReturn() bool {
	return !b.terminates && !b.valueInStmts
}

// noValueBranch is a branch whose statements stmts produce no value: its
// expr is the placeholder `nil`.
func noValueBranch(stmts []ast.Stmt) loweredIfBranch {
	return loweredIfBranch{stmts: stmts, expr: ast.NewIdent("nil"), noValue: true}
}

// checkIfBranchValues rejects a branch of an if-expression whose value is
// used that produces no value — nothing could be returned for it. A branch
// that terminates or leaves needs no value. typed says the if-expression has
// a type: then every other branch needs a value. Otherwise one is needed only
// next to a branch that has one; with none, the if-expression has no type at
// all, which GALA-E0068 reports.
func (t *galaASTTransformer) checkIfBranchValues(branches [2]ifBranchSrc, lowered [2]loweredIfBranch, typed bool) error {
	missing := -1
	valued := false
	for i, b := range lowered {
		switch {
		case !b.needsValueReturn() || t.leaves(b.stmts):
			valued = valued || b.valueInStmts
		case b.noValue || (t.getExprTypeName(b.expr).IsVoid() && !t.isNoReturnCallExpr(b.expr)):
			if missing < 0 {
				missing = i
			}
		default:
			valued = true
		}
	}
	if missing >= 0 && (typed || valued) {
		return t.noValueBranchError(branches[missing].at())
	}
	return nil
}

// noValueBranchError reports that the branch at at, of an if-expression whose
// value is used, produces no value.
func (t *galaASTTransformer) noValueBranchError(at srcNode) error {
	return t.semanticErrorAtNode(at,
		"this branch of the if-expression produces no value, but the if-expression's value is used; end the branch in a value")
}

// hoistIfExpression lowers an if-expression whose value s.hoist stores, from
// its lowered branches, as an if statement storing each branch's value: a
// branch that terminates (ends in a `return`, `break` or `continue`) stores
// none. The value's type is that of the branches that store one (see
// hoistedType).
func (t *galaASTTransformer) hoistIfExpression(e ifExprSrc, cond ast.Expr, s slot, branches [2]loweredIfBranch) (ast.Expr, error) {
	line, col := e.at().start()
	if err := t.checkIfBranchValues(e.branches(), branches, false); err != nil {
		return nil, err
	}
	var types [2]transpiler.Type
	for i, b := range branches {
		// A branch ending in an if/else every path of which leaves has no
		// value either.
		if t.leaves(b.stmts) {
			branches[i].terminates, b.terminates = true, true
		}
		types[i] = transpiler.NilType{}
		if !b.terminates {
			types[i] = t.getExprTypeName(b.expr)
		}
	}
	allLeave := branches[0].terminates && branches[1].terminates
	typ, err := t.hoistedType("if-expression", t.unifyBranchTypes(types[0], types[1]), s, allLeave,
		line, col, branches[0].stmts, branches[1].stmts)
	if err != nil {
		return nil, err
	}
	store := func(b loweredIfBranch) *ast.BlockStmt {
		switch {
		case b.valueInStmts:
			// Each branch of its trailing if/else chain stores its value.
			return &ast.BlockStmt{List: t.storeArmValues(b.stmts, s.hoist, typ)}
		case !b.terminates && t.isNoReturnCallExpr(b.expr):
			return &ast.BlockStmt{List: append(b.stmts, &ast.ExprStmt{X: b.expr})}
		case !b.terminates:
			return &ast.BlockStmt{List: append(b.stmts, t.storeValue(b.expr, s.hoist, typ)...)}
		}
		return &ast.BlockStmt{List: b.stmts}
	}
	ifStmt := &ast.IfStmt{Cond: cond, Body: store(branches[0]), Else: store(branches[1])}
	return t.hoistedResult(s.hoist, []ast.Stmt{ifStmt}, typ, line, col)
}

// isNilIdent reports whether expr is the identifier `nil`.
func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// transformIfExprBranch lowers an if-expression branch, an expression or a
// block, against s, the if-expression's slot (see lowerAgainst). A block's
// value is its trailing expression, or its trailing if/else chain; its other
// statements run first. A branch that already terminates (it ends in an
// explicit `return`) gets no synthesized `return <expr>`: that would be dead
// code returning a placeholder (see loweredIfBranch.needsValueReturn).
func (t *galaASTTransformer) transformIfExprBranch(ctx *grammar.IfExprBranchContext, s slot) (loweredIfBranch, error) {
	return t.lowerIfExprBranch(t.ifBranchOf(ctx), s)
}

// lowerIfExprBranchBlock lowers b, an if-expression branch that is a block,
// against s (see transformIfExprBranch).
func (t *galaASTTransformer) lowerIfExprBranchBlock(b blockSrc, s slot) (loweredIfBranch, error) {
	// Block branch: transform all statements, use last as the result expression.
	stmts := b.statements()
	if len(stmts) == 0 {
		return noValueBranch(nil), nil
	}

	// A `bind` takes over the rest of the branch (see lowerIfExprBranchBind).
	bindAt := slices.IndexFunc(stmts, func(s stmtSrc) bool { return s.kind == stmtBind })
	end := len(stmts) - 1
	if bindAt >= 0 {
		end = bindAt
	}

	// The statements before the value discard their values, as in any block.
	var preceding []ast.Stmt
	for _, st := range stmts[:end] {
		t.noteNativeStmt(st)
		lowered, err := t.lowerDiscardedStatement(st)
		if err != nil {
			return loweredIfBranch{}, err
		}
		preceding = append(preceding, lowered...)
	}
	if bindAt >= 0 {
		expr, err := t.lowerIfExprBranchBind(stmts[bindAt:], s)
		if err != nil {
			return loweredIfBranch{}, err
		}
		return loweredIfBranch{stmts: preceding, expr: expr}, nil
	}

	// The branch's value is its last statement's expression.
	last := stmts[len(stmts)-1]
	t.noteNativeStmt(last)
	if value := last.valueExpr(); value.ok() {
		// The branch's value is its trailing expression; loop control has none.
		if bs, ok := t.lowerLoopControl(value); ok {
			if s.hoist != "" {
				return loweredIfBranch{stmts: append(preceding, bs), terminates: true}, nil
			}
			return loweredIfBranch{}, t.loopControlInValueError("an if-expression", bs)
		}
		expr, err := t.lowerResultAgainst(value, s, true)
		if err != nil {
			return loweredIfBranch{}, err
		}
		return loweredIfBranch{stmts: preceding, expr: expr}, nil
	}

	// A trailing if/else chain carries the branch's value in its branches, as
	// at the tail of a match arm.
	if ifS := last.ifStmt(); ifS.ok() {
		b, err := t.lowerIfExprBranchTailIf(ifS, s)
		if err != nil {
			return loweredIfBranch{}, err
		}
		b.stmts = append(preceding, b.stmts...)
		return b, nil
	}

	// If the last statement isn't a bare expression, transform it normally
	// and return nil as the expression (void block).
	lastStmt, pre, err := t.lowerStatement(last)
	if err != nil {
		return loweredIfBranch{}, err
	}
	preceding = append(append(preceding, pre...), lastStmt)
	// If the last statement is an explicit return, the branch terminates: the
	// caller must skip its synthesized trailing return so we don't emit dead
	// `return nil` after a typed return. We also surface the return's value
	// expression as the branch expression so the IIFE result type can be
	// inferred from it (rather than defaulting to `any`).
	if retStmt, ok := lastStmt.(*ast.ReturnStmt); ok && last.kind == stmtReturn {
		var branchExpr ast.Expr = ast.NewIdent("nil")
		if len(retStmt.Results) > 0 {
			branchExpr = retStmt.Results[0]
		}
		return loweredIfBranch{stmts: preceding, expr: branchExpr, terminates: true}, nil
	}
	return noValueBranch(preceding), nil
}

// lowerIfExprBranchValue lowers e, an if-expression branch that is an
// expression, against s (see transformIfExprBranch).
func (t *galaASTTransformer) lowerIfExprBranchValue(e exprSrc, s slot) (loweredIfBranch, error) {
	if bs, ok := t.lowerLoopControl(e); ok {
		if s.hoist != "" {
			// Lowered as statements, the branch acts on the loop: it
			// terminates, with no value.
			return loweredIfBranch{stmts: []ast.Stmt{bs}, terminates: true}, nil
		}
		return loweredIfBranch{}, t.loopControlInValueError("an if-expression", bs)
	}
	expr, err := t.lowerResultAgainst(e, s, true)
	if err != nil {
		return loweredIfBranch{}, err
	}
	return loweredIfBranch{expr: expr}, nil
}

// lowerIfExprBranchTailIf lowers the if/else chain ifS at the tail of an
// if-expression branch filling slot s. When each branch of the chain ends in
// a value, the chain returns it from each branch; otherwise the chain is a
// statement and the if-expression branch has no value.
func (t *galaASTTransformer) lowerIfExprBranchTailIf(ifS ifSrc, s slot) (loweredIfBranch, error) {
	promoted, ok, err := t.lowerTailIf(ifS, s, t.armReturn)
	if err != nil {
		return loweredIfBranch{}, err
	}
	if !ok {
		if err := t.checkUnpromotedBranchTails(ifS, promoted); err != nil {
			return loweredIfBranch{}, err
		}
		return noValueBranch([]ast.Stmt{promoted}), nil
	}
	if err := t.checkChainValueTypes(ifS.at(), promoted); err != nil {
		return loweredIfBranch{}, err
	}
	result := t.armBranchResult(promoted, s)
	if result == nil {
		// No branch gives a value: each one leaves.
		return loweredIfBranch{stmts: []ast.Stmt{promoted}, terminates: true}, nil
	}
	return loweredIfBranch{stmts: []ast.Stmt{promoted}, expr: result, valueInStmts: true}, nil
}

// checkChainValueTypes rejects a promoted if/else chain (written at at)
// whose branch values have types that do not unify: each is a value of the
// one block the chain ends.
func (t *galaASTTransformer) checkChainValueTypes(at srcNode, promoted *ast.IfStmt) error {
	var unified transpiler.Type = transpiler.NilType{}
	var err error
	ast.Inspect(promoted, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok || err != nil {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 || !t.isSynthesizedArmReturn(ret) {
			return true
		}
		typ := t.getExprTypeName(ret.Results[0])
		if transpiler.IsUnusableOrAny(typ) || typ.IsVoid() {
			return true
		}
		if transpiler.IsUnusable(unified) {
			unified = typ
		} else if next := t.unifyBranchTypes(unified, typ); !next.IsNil() {
			unified = next
		} else {
			err = t.semanticErrorAtNode(at, fmt.Sprintf(
				"the branches of this `if` end in values of different types, %s and %s; end them in values of one type", unified, typ))
		}
		return true
	})
	return err
}

// lowerIfExprBranchBind lowers stmts, the rest of an if-expression branch from
// its first `bind`, to a FlatMap chain over the branch's monad: the type of the
// slot s the if-expression fills, or, when that is not known, the branch's
// trailing value. Never the enclosing function's result type — the
// if-expression's value need not be what the function returns. The chain's
// bound names are local to the branch.
func (t *galaASTTransformer) lowerIfExprBranchBind(stmts []stmtSrc, s slot) (ast.Expr, error) {
	t.noteNativeStmt(stmts[0])
	if err := t.checkNoEscapeAfterBind(stmts, "an if-expression branch"); err != nil {
		return nil, err
	}
	typ := s.closedType()
	t.pushScope()
	defer t.popScope()
	// A hoisted if-expression enters no IIFE slot of its own, so the chain's
	// result would otherwise come from the enclosing function's.
	defer t.enterIIFEReturnSlot(typ)()
	return t.desugarBindChain(stmts, t.newBindResult(typ))
}

// closedType is the slot's type, or nil when the slot is open: an open type's
// placeholders are no result type.
func (s slot) closedType() transpiler.Type {
	if s.open {
		return nil
	}
	return s.typ
}

// slot is the type a value is lowered against (see lowerAgainst), with the
// way the value is lowered: an open, by-name or discarded slot.
type slot struct {
	typ transpiler.Type
	// tryThunk: the slot is the thunk parameter of Try(...), which turns an
	// error into a Failure, so a Go call there yields its plain value and
	// panics on the error rather than producing a Try (see tryThunkValue).
	tryThunk bool
	// discarded: nothing reads the value filling the slot — the arms of a
	// statement-position match. A block filling it lowers its trailing match
	// or if as a statement, not as the block's value (see transformValueBlock).
	discarded bool
	// open: typ may hold placeholders for type parameters the call left
	// unbound (an `any` fill, see inferFuncTypeSubstFromArgs, or the generic
	// method path's default-to-any view). An open slot type never overrides
	// the branches' own type (see branchingResultType); user-written `any` is
	// not a placeholder and does not make a slot open.
	open bool
	// hoist: the value is a local declaration's initializer, or an arm of
	// one lowered as statements, and is stored in this variable: a match or
	// if-expression whose arms hold a `return`, `break` or `continue` is
	// lowered as statements storing its value there (see hoisted_value.go).
	hoist string
}

// typedSlot is the slot of type typ: an argument, a declaration, a function
// or lambda result. Each gives typ to the value that fills it, and only to it
// (see consumesSlotType).
func typedSlot(typ transpiler.Type) slot { return slot{typ: typ} }

// exprForm classifies an expression by the forms whose lowering depends on
// the slot they fill; at most one field is set.
type exprForm struct {
	grouped exprSrc // e for `(e)`
	lambda  lambdaSrc
	partial partialSrc
	ifExpr  ifExprSrc
	match   postfixSrc
}

// classifyExpr reads e's form, on e's tree.
func (t *galaASTTransformer) classifyExpr(e exprSrc) exprForm {
	if !e.ok() {
		return exprForm{}
	}
	if e.node.Ok() {
		return t.classifyNative(e)
	}
	if inner := t.grouped(e); inner.ok() {
		return exprForm{grouped: inner}
	}
	if l := t.lambdaIn(e); l.ok() {
		return exprForm{lambda: l}
	}
	if pf := t.barePartial(e); pf.ok() {
		return exprForm{partial: pf}
	}
	if i := t.ifIn(e); i.ok() {
		return exprForm{ifExpr: i}
	}
	return exprForm{match: t.bareMatch(e)}
}

// needsExpectedType reports whether e is a lambda, a partial function, an
// if-expression or a match, possibly parenthesized.
func (t *galaASTTransformer) needsExpectedType(e exprSrc) bool {
	f := t.classifyExpr(e)
	if f.grouped.ok() {
		return t.needsExpectedType(f.grouped)
	}
	return f.lambda.ok() || f.partial.ok() || f.ifExpr.ok() || f.match.ok()
}

// lowerAgainst lowers e in check mode against the slot it fills. A lambda
// takes its parameter and result types from a function type (strict is
// transformLambdaWithExpectedType's untyped-parameter policy), and so does a
// partial function (see transformPartialFunctionLiteral); an if-expression
// or match lowers each branch against the same slot (branch lambdas strictly);
// a plain expression sees the slot type only if it consumes it
// (consumesSlotType). A lambda body is never lowered against the outer slot:
// it gets typedSlot(the lambda's result type).
func (t *galaASTTransformer) lowerAgainst(e exprSrc, s slot, strict bool) (ast.Expr, error) {
	e = t.onNative(e)
	untyped := transpiler.IsUnusable(s.typ)
	if untyped && s.hoist == "" {
		return t.lowerExpr(e)
	}
	f := t.classifyExpr(e)
	if untyped && !t.hoistableForm(f) {
		return t.lowerExpr(e)
	}
	if e.node.Ok() {
		t.nativeShape(e.node, "slot")
	}
	switch {
	case f.grouped.ok():
		expr, err := t.lowerAgainst(f.grouped, s, strict)
		if err != nil {
			return nil, err
		}
		return &ast.ParenExpr{X: expr}, nil
	case f.lambda.ok():
		if expectedRetType, expectedParamTypes, ok := t.lambdaExpectation(s.typ); ok {
			lit, err := t.lowerLambda(f.lambda, expectedRetType, expectedParamTypes, strict, t.declaresAnyResult(s))
			if err != nil {
				return nil, err
			}
			return t.spreadLambdaGoResults(lit, s.typ, f.lambda.at())
		}
		return t.lowerExpr(e)
	case f.partial.ok():
		return t.transformPartialFunctionLiteral(f.partial, t.partialFunctionSlot(s))
	case f.ifExpr.ok():
		return t.lowerIfExpression(f.ifExpr, s)
	case f.match.ok():
		return t.lowerMatch(f.match, s)
	}
	// A tuple literal filling a slot (`func f() Tuple[int64, int64] = (1, 2)`)
	// takes its element types from it, and so does a construction of the
	// generic struct the slot names (`func f() Tag[int] = Tag("x")`), for the
	// type arguments its fields leave open, and a generic function or method
	// call whose type parameters the slot can give (`func f() Option[int] =
	// parse()`). They are the only plain expressions a slot pushes for
	// (consumesSlotType), and only the literal or the call that is the value
	// reads the entry: its receiver does not (expectedArgTypeStack.withhold),
	// and the call takes it before its arguments are lowered, so nothing
	// nested inside it sees the slot type.
	//
	// The hint is the type an alias names, not the alias: the constructors and
	// generic calls that read it match its structure (`Try[Email]` for
	// `type Checked Try[Email]`) to bind their type arguments.
	if hint := t.followAliasChain(s.typ); t.consumesSlotType(e, hint) {
		release := t.expectedArgTypes.pushFor(hint, t.hintKey(e.solePostfix()))
		defer release()
	}
	expr, err := t.lowerExpr(e)
	if err != nil {
		return nil, err
	}
	// A Go call converted to a Try or Tuple where its plain value is expected
	// is named here, not left to Go's type mismatch on the generated code.
	if !s.open && !s.tryThunk {
		if err := t.checkGoResultAgainst(expr, s.typ, e.at()); err != nil {
			return nil, err
		}
	}
	// An opaque type and its underlying type (or another opaque type) never
	// convert implicitly; say so in GALA rather than leave it to Go. An open
	// slot is not a type the value has to have.
	if !s.open {
		if err := t.checkOpaqueMismatch(expr, s.typ, e.at()); err != nil {
			return nil, err
		}
		// A nil, or a pointer that may be nil, never fills a Ptr.
		if err := t.checkPtrSlot(expr, s.typ, e.at()); err != nil {
			return nil, err
		}
	}
	return expr, nil
}

// lowerResultAgainst is lowerAgainst for a result value: the value a function
// or lambda returns (`return v`, its expression body, its trailing value), or
// a branch of an if-expression or an arm of a match, which its IIFE returns
// wherever the construct itself sits. A bare `nil` there takes the slot's
// type, as Go gives an untyped nil the type of the result it is returned as.
// `nil` has no type of its own, so whatever asks for the value's type
// afterwards — the void check on a function's trailing value, the branch
// unification of an if-expression, the common type of a match's arms — would
// otherwise find none.
//
// A `nil` that is itself a call argument or a tuple element is lowered by
// lowerAgainst and keeps no type: the call reads it as untyped (an explicit
// nil to a by-name parameter is a thunk returning nil, and a generic call
// spells its type argument out).
func (t *galaASTTransformer) lowerResultAgainst(e exprSrc, s slot, strict bool) (ast.Expr, error) {
	expr, err := t.lowerAgainst(e, s, strict)
	if err != nil {
		return nil, err
	}
	// The type is cached on the expression node, which is fresh for each
	// `nil` written. A slot type nil cannot have gives it nothing, so the
	// mismatch stays visible.
	if isNilIdent(expr) && !s.open && t.holdsNil(s.typ) {
		t.exprTypeCache[expr] = s.typ
	}
	return expr, nil
}

// holdsNil reports whether typ is a type `nil` can have: a pointer, slice,
// map, function, or an interface other than `any`. A Ptr is a struct, never
// nil.
func (t *galaASTTransformer) holdsNil(typ transpiler.Type) bool {
	if typeHasMaskedPart(typ) || typ.IsAny() {
		return false
	}
	switch t.followAliasChain(typ).(type) {
	case transpiler.PointerType, transpiler.ArrayType, transpiler.MapType, transpiler.FuncType:
		return true
	}
	return t.isInterfaceType(typ)
}

// branchingResultType picks the result type of an if-expression or match from
// its branches' inferred type and its slot. The slot type is the fallback when
// inference fails. It also wins over function-typed branches (it is what they
// were lowered against, and its spelling is what the value must be assignable
// to), unless the slot is open or has a masked (nil) part: then the branches'
// own type is the concrete one. Branches of a non-function type keep theirs: a
// by-name thunk slot (`Future(x match {...})`) receives a value, not a function.
func (t *galaASTTransformer) branchingResultType(inferred transpiler.Type, s slot) transpiler.Type {
	if transpiler.IsUnusable(s.typ) {
		return inferred
	}
	if transpiler.IsUnusable(inferred) {
		return s.typ
	}
	// A branch type naming a type parameter nothing bound (the `T` of
	// `Try[T]`) is not a type the generated Go can spell; a concrete slot is.
	if !s.open && !typeHasMaskedPart(s.typ) && t.typeMentionsUnresolvedTypeParam(inferred) {
		return s.typ
	}
	if !s.open && !typeHasMaskedPart(s.typ) &&
		t.resolveTranspilerTypeAsFuncType(s.typ) != nil && t.resolveTranspilerTypeAsFuncType(inferred) != nil {
		return s.typ
	}
	return inferred
}

// typeHasMaskedPart reports whether typ contains a nil part: a type parameter
// masked out as unresolved (see maskTypeParamResults). Unlike `any`, nil is
// never written in GALA source.
func typeHasMaskedPart(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return true
	}
	switch v := typ.(type) {
	case transpiler.FuncType:
		return slices.ContainsFunc(v.Params, typeHasMaskedPart) || slices.ContainsFunc(v.Results, typeHasMaskedPart)
	case transpiler.GenericType:
		return slices.ContainsFunc(v.Params, typeHasMaskedPart)
	case transpiler.ArrayType:
		return typeHasMaskedPart(v.Elem)
	case transpiler.ChanType:
		return typeHasMaskedPart(v.Elem)
	case transpiler.StructType:
		return slices.ContainsFunc(v.FieldTypes(), typeHasMaskedPart)
	case transpiler.PointerType:
		return typeHasMaskedPart(v.Elem)
	case transpiler.MapType:
		return typeHasMaskedPart(v.Key) || typeHasMaskedPart(v.Elem)
	}
	return false
}

// consumesSlotType reports whether e is a plain expression that takes
// the type of the slot it fills (see lowerAgainst) — an argument, a
// declaration, a function or lambda result — given as hint, the type an alias
// names: a tuple literal of that tuple type, a construction of a value of
// that generic type (isConstructionOf), a call of a generic function whose
// result mentions its type parameters (isResultGenericCall),
// or a call of a method whose own type parameters its result mentions
// (isResultGenericMethodCall).
//
// This is the one place a slot type reaches a value's type arguments, and it
// reaches only the call or literal that is the value: never a receiver it is
// applied to (see expectedArgTypeStack.withhold), an argument nested in it,
// or a value elsewhere in the enclosing function.
func (t *galaASTTransformer) consumesSlotType(e exprSrc, hint transpiler.Type) bool {
	if t.isTupleLiteralFor(e, hint) {
		return true
	}
	name, typeArgs, noArgs := calleeOfCall(e)
	if name != "" && (t.isConstructionOf(name, hint) || !typeArgs && t.isResultGenericCall(name, noArgs)) {
		return true
	}
	return t.isResultGenericMethodCall(e)
}

// isResultGenericMethodCall reports whether e is a method call written
// without type arguments, `recv.m(...)`, where some type declares a method m
// whose own type parameters its result mentions (`Convert[U any]() Option[U]`,
// `Map[U any](f func(T) U) Option[U]`): the slot the call fills can bind
// them. The receiver's type is not known before it is lowered, so any such m
// qualifies; the method call itself takes the slot type only when it is one.
func (t *galaASTTransformer) isResultGenericMethodCall(e exprSrc) bool {
	p := e.solePostfix()
	if !p.ok() || p.hasCases() {
		return false
	}
	suffixes := p.suffixes()
	n := len(suffixes)
	if n < 2 || suffixes[n-1].opener() != "(" {
		return false
	}
	// The method is the member the suffix before the call selects.
	member, _, ok := suffixes[n-2].member()
	return ok && t.resultGenericMethods[member]
}

// isTupleLiteralFor reports whether e is exactly a tuple literal
// `(a, b, ...)` whose arity matches the tuple type typ.
func (t *galaASTTransformer) isTupleLiteralFor(e exprSrc, typ transpiler.Type) bool {
	gen, ok := typ.(transpiler.GenericType)
	if !ok || !t.isTupleTypeName(gen.Base.String()) {
		return false
	}
	_, n := t.parenthesized(e)
	return n > 1 && n == len(gen.Params)
}

// isConstructionOf reports whether a call of name (see calleeOfCall) is a
// construction of a value of the generic type typ instantiates — of that
// struct (`Tag("x")`, `Pair[int](1)`, `geo.Tag(name = "x")` for `Tag[int]`),
// or through a companion Apply returning it (`Mk(1)` for a
// `Pair[int, string]` when `Mk[A, B]`'s Apply returns `Pair[A, B]`;
// `Left("x")` for an `Either[string, int]`). calleeOfCall rules out a value
// derived from one (`Tag("x").Rename()`).
func (t *galaASTTransformer) isConstructionOf(name string, typ transpiler.Type) bool {
	gen, ok := typ.(transpiler.GenericType)
	if !ok {
		return false
	}
	// `Tuple(a, b, c)` constructs the std tuple of its arity, `Tuple3`
	// (rewriteStdTupleIdent).
	if stripPackagePrefix(name) == transpiler.TypeTuple && isTupleTypeName(stripPackagePrefix(gen.Base.BaseName())) {
		return true
	}
	// Most result values call a function, not a type: that settles it before
	// the slot's type is resolved.
	resolved := t.resolveTypeMetaName(name)
	if resolved == "" {
		return false
	}
	// A generic alias of a struct (`Twin(1, 2)` for `type Twin[T any]
	// Pair[T]`) constructs that struct: its type arguments come from the
	// struct's (see aliasLiteralType).
	meta := t.typeMetas[resolved]
	if t.isGenericStructAlias(name, resolved, meta) {
		target, _ := t.lookupTypeAlias(name)
		want := t.resolveTypeMetaName(gen.Base.String())
		return want != "" && t.resolveTypeMetaName(t.followAliasChain(target).BaseName()) == want
	}
	// Compared by their metadata keys: the field map has both a bare and a
	// package-qualified key for a type of this package (`Q`, `units.Q`).
	if _, isStruct := t.structFields[resolved]; isStruct && stripPackagePrefix(name) == stripPackagePrefix(gen.Base.BaseName()) {
		return resolved == t.resolveTypeMetaName(gen.Base.String())
	}
	if meta == nil {
		return false
	}
	apply, hasApply := meta.Methods["Apply"]
	if !hasApply {
		return false
	}
	ret, ok := apply.ReturnType.(transpiler.GenericType)
	if !ok {
		return false
	}
	want := t.resolveTypeMetaName(gen.Base.String())
	return want != "" && t.resolveTypeMetaName(ret.Base.String()) == want
}

// calleeOfCall returns the callee of an expression that is exactly one call
// of a name, optionally package-qualified and with type arguments (`Tag("x")`,
// `geo.Tag[int](1)`, `parse()`), as written without its type arguments, and
// whether it has any type arguments and whether it passes no arguments; ""
// for anything else.
func calleeOfCall(e exprSrc) (name string, typeArgs, noArgs bool) {
	if p := e.solePostfix(); !p.ok() || p.hasCases() {
		return "", false, false
	}
	pe := patExpr{exprSrc: e}
	if c, ok := pe.callPattern(); ok {
		return c.head.primaryText(), c.hasTypeArgList(), len(c.args.list) == 0
	}
	if c, ok := pe.qualifiedCallPattern(); ok {
		return c.head.primaryText() + "." + c.ctor, c.hasTypeArgList(), len(c.args.list) == 0
	}
	return "", false, false
}

// isResultGenericCall reports whether name (see calleeOfCall, called without
// type arguments) is a generic function whose result mentions one of its type
// parameters: the slot the call fills gives one only its result mentions
// (`parse()` for `func parse[T any]() Option[T]`), and one only untyped
// constant arguments bind (`Id(0)` in an `int64` slot), which Go would infer
// as the constant's default type (see injectFuncPhantomTypeArgs). It also
// gives every type parameter of a call with noArgs whose parameters all take
// their defaults (`val p Option[int] = pick()` for
// `func pick[T any](d Option[T] = None[T]()) Option[T]`).
func (t *galaASTTransformer) isResultGenericCall(name string, noArgs bool) bool {
	meta := t.getFunction(name)
	if meta == nil || len(meta.TypeParams) == 0 {
		return false
	}
	if noArgs && len(meta.ParamTypes) > 0 && len(meta.DefaultExprs) == len(meta.ParamTypes) {
		return true
	}
	mentioned := make(map[string]bool)
	t.collectReferencedParams(meta.ReturnType, meta.TypeParams, mentioned)
	return len(mentioned) > 0
}

// isGenericStructAlias reports whether name, resolving to the typeMetas key
// resolved with metadata meta, is a generic alias of a plain (not sealed)
// struct — a name with fields of its own is the struct, whatever local alias
// shares its bare name.
func (t *galaASTTransformer) isGenericStructAlias(name, resolved string, meta *transpiler.TypeMetadata) bool {
	if meta == nil || len(meta.TypeParams) == 0 || len(t.structFields[resolved]) > 0 {
		return false
	}
	target, isAlias := t.lookupTypeAlias(name)
	if !isAlias {
		return false
	}
	end := t.followAliasChain(target).BaseName()
	endMeta := t.getTypeMeta(t.resolveTypeMetaName(end))
	_, isStruct := t.structFields[t.resolveStructTypeName(end)]
	return endMeta != nil && !endMeta.IsSealed && isStruct
}

// unwrapImmutable is the single canonical unwrap helper for val-wrapped
// Immutable[T] values. Given an expression of type Immutable[T], it returns
// a .Get() call to produce the underlying T; otherwise it returns the
// expression unchanged (pure type names and non-Immutable values are
// pass-through). All transformer paths that need to read through an
// Immutable wrapper MUST route through this function — do not re-implement
// the logic inline.
func (t *galaASTTransformer) unwrapImmutable(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return &ast.ParenExpr{
			X: t.unwrapImmutable(paren.X),
		}
	}

	// Don't unwrap if it's a type name (identifier or selector)
	if ident, ok := expr.(*ast.Ident); ok {
		// `nil` is a keyword, never a val, so it is never wrapped; asking for
		// its type (`err == nil` unwraps both operands) only finds none.
		if ident.Name == "nil" {
			return expr
		}
		if !t.isVal(ident.Name) && !t.isVar(ident.Name) {
			if !t.lookupTypeName(ident.Name).IsNil() {
				return expr
			}
			// Nor a function name, for the same reason. This arrives via
			// resolveIndexAccess: `ArrayOf[Tuple[bool, string]](...)` parses as
			// an index access, so the base is probed for an Immutable wrapper
			// on the way past. Asking is not free — a generic function's ident
			// resolves to NilType, which getExprTypeNameManual declines to
			// cache, so every such query re-runs the walk.
			//
			// getFunction is checked last: it builds a resolver and an imports
			// slice per call, so the cheap scope and type lookups go first.
			if t.getFunction(ident.Name) != nil {
				return expr
			}
		}
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if xIdent, ok := sel.X.(*ast.Ident); ok {
			fullPath := xIdent.Name + "." + sel.Sel.Name
			if !t.isVal(fullPath) && !t.isVar(fullPath) {
				if !t.lookupTypeName(fullPath).IsNil() {
					return expr
				}
			}
		}
	}

	typeObj := t.getExprTypeName(expr)
	if t.isImmutableType(typeObj) {
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   expr,
				Sel: ast.NewIdent(transpiler.MethodGet),
			},
		}
	}
	return expr
}
