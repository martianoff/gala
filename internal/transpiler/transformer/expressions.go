package transformer

import (
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"slices"
)

// NOTE: transformCallExpr was removed - it was dead code.
// Call transformation goes through transformCallWithArgsCtx.

func (t *galaASTTransformer) transformExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
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
		if result, err = t.binaryOperation("||", token.LOR, ctx, result, right); err != nil {
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
		if result, err = t.binaryOperation("&&", token.LAND, ctx, result, right); err != nil {
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
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), ctx, result, right); err != nil {
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
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), ctx, result, right); err != nil {
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
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), ctx, result, right); err != nil {
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
		if result, err = t.binaryOperation(opText, t.getBinaryToken(opText), ctx, result, right); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) transformUnaryExpr(ctx *grammar.UnaryExprContext) (ast.Expr, error) {
	// Check for unary operator
	if unaryOp := ctx.UnaryOp(); unaryOp != nil {
		opText := unaryOp.GetText()

		// For address-of operator, check if operand is a val before transforming
		// This is needed because transforming a val normally results in name.Get()
		// which is not addressable. We need to call name.Ptr() instead.
		// We wrap the result in ConstPtr to prevent write-through.
		if opText == "&" {
			if valName := t.getSimpleValIdentifier(ctx.UnaryExpr().(*grammar.UnaryExprContext)); valName != "" {
				// Generate: std.NewConstPtr(valName.Ptr())
				return &ast.CallExpr{
					Fun: t.stdIdent(transpiler.FuncNewConstPtr),
					Args: []ast.Expr{
						&ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   ast.NewIdent(valName),
								Sel: ast.NewIdent(transpiler.MethodPtr),
							},
						},
					},
				}, nil
			}
		}

		innerUnary := ctx.UnaryExpr()
		expr, err := t.transformUnaryExpr(innerUnary.(*grammar.UnaryExprContext))
		if err != nil {
			return nil, err
		}
		// A parameter not declared `var`, and any receiver, is immutable like a
		// val but is a plain Go parameter, so its address is taken directly and
		// wrapped in ConstPtr to prevent write-through: std.NewConstPtr(&name).
		if opText == "&" && t.fixedBindingOf(simpleIdentifierName(innerUnary.(*grammar.UnaryExprContext))) != notFixed {
			return &ast.CallExpr{
				Fun:  t.stdIdent(transpiler.FuncNewConstPtr),
				Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: expr}},
			}, nil
		}
		if opText == "*" {
			// Check if we're dereferencing a ConstPtr - if so, call Deref() instead
			typeObj := t.getExprTypeName(expr)
			if t.isConstPtrType(typeObj) {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   expr,
						Sel: ast.NewIdent(transpiler.MethodDeref),
					},
				}, nil
			}
			return &ast.StarExpr{X: expr}, nil
		}
		if opText == "!" {
			expr = t.wrapWithAssertion(expr, ast.NewIdent("bool"))
		}
		// For address-of operator on immutable values, call Ptr() and wrap in ConstPtr
		if opText == "&" {
			typeObj := t.getExprTypeName(expr)
			if t.isImmutableType(typeObj) {
				// Generate: std.NewConstPtr(expr.Ptr())
				return &ast.CallExpr{
					Fun: t.stdIdent(transpiler.FuncNewConstPtr),
					Args: []ast.Expr{
						&ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   expr,
								Sel: ast.NewIdent(transpiler.MethodPtr),
							},
						},
					},
				}, nil
			}
			return &ast.UnaryExpr{Op: token.AND, X: expr}, nil
		}
		// Automatic unwrapping for other unary operands
		expr = t.unwrapImmutable(expr)
		return &ast.UnaryExpr{Op: t.getUnaryToken(opText), X: expr}, nil
	}

	// Otherwise it's a postfixExpr
	if postfix := ctx.PostfixExpr(); postfix != nil {
		return t.transformPostfixExpr(postfix.(*grammar.PostfixExprContext))
	}

	return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "unaryExpr must have unaryOp or postfixExpr")
}

// getSimpleValIdentifier extracts the identifier name if this unary expression
// is a simple identifier reference to a val variable (no suffixes).
// Returns empty string if not a simple val identifier.
func (t *galaASTTransformer) getSimpleValIdentifier(ctx *grammar.UnaryExprContext) string {
	if name := simpleIdentifierName(ctx); name != "" && t.isVal(name) {
		return name
	}
	return ""
}

// simpleIdentifierName returns the identifier a unary expression consists of
// alone (no operator, no suffixes), or "" for anything else.
func simpleIdentifierName(ctx *grammar.UnaryExprContext) string {
	// Must not have a unary operator
	if ctx.UnaryOp() != nil {
		return ""
	}
	postfix := ctx.PostfixExpr()
	if postfix == nil {
		return ""
	}
	postfixCtx := postfix.(*grammar.PostfixExprContext)
	// Must not have any suffixes (calls, member access, etc.)
	if len(postfixCtx.AllPostfixSuffix()) > 0 {
		return ""
	}
	primaryExpr := postfixCtx.PrimaryExpr()
	if primaryExpr == nil {
		return ""
	}
	primaryExprCtx, ok := primaryExpr.(*grammar.PrimaryExprContext)
	if !ok {
		return ""
	}
	primary := primaryExprCtx.Primary()
	if primary == nil {
		return ""
	}
	primaryCtx, ok := primary.(*grammar.PrimaryContext)
	if !ok {
		return ""
	}
	if primaryCtx.Identifier() == nil {
		return ""
	}
	return primaryCtx.Identifier().GetText()
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

// Postfix-related functions moved to postfix.go
func (t *galaASTTransformer) transformExpressionList(ctx *grammar.ExpressionListContext) ([]ast.Expr, error) {
	var exprs []ast.Expr
	for _, eCtx := range ctx.AllExpression() {
		e, err := t.transformExpression(eCtx)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	return exprs, nil
}

// transformExpressionListAgainst lowers a declaration's or assignment's
// right-hand side; a single expression is lowered against typedSlot(expected).
func (t *galaASTTransformer) transformExpressionListAgainst(ctx *grammar.ExpressionListContext, expected transpiler.Type) ([]ast.Expr, error) {
	exprs := ctx.AllExpression()
	if len(exprs) != 1 {
		return t.transformExpressionList(ctx)
	}
	e, err := t.lowerAgainst(exprs[0], typedSlot(expected), true)
	if err != nil {
		return nil, err
	}
	return []ast.Expr{e}, nil
}

// binaryOperation builds `left op right` from two lowered operands: it rejects
// a Go call's Try or Tuple as an operand (GALA-E0049) and reads vals through
// their Immutable wrapper.
func (t *galaASTTransformer) binaryOperation(op string, tok token.Token, ctx antlr.ParserRuleContext, left, right ast.Expr) (ast.Expr, error) {
	if err := t.checkGoResultOperands(op, ctx, left, right); err != nil {
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

// getPrimaryFromExpression navigates the new grammar structure to find the primary
// This is used for backward compatibility with code that expects expr.Primary()
func (t *galaASTTransformer) getPrimaryFromExpression(ctx grammar.IExpressionContext) *grammar.PrimaryContext {
	return PrimaryOf(LeadingPostfixExpr(ctx, false))
}

// LeadingPostfixExpr walks the precedence chain (or → and → equality →
// relational → additive → multiplicative → unary) down to the postfix
// expression of ctx's first operand, or nil when a unary operator or no
// operand intervenes. With sole set, every level must hold exactly one
// operand, i.e. ctx has no binary operator at all.
func LeadingPostfixExpr(ctx grammar.IExpressionContext, sole bool) *grammar.PostfixExprContext {
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
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
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

// PrimaryOf returns the primary a postfix expression starts from, or nil when
// it starts from a lambda, if-expression or partial function instead.
func PrimaryOf(postfix *grammar.PostfixExprContext) *grammar.PrimaryContext {
	if postfix == nil {
		return nil
	}
	primaryExpr, _ := postfix.PrimaryExpr().(*grammar.PrimaryExprContext)
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

// getQualifiedCallPattern detects a package-qualified constructor pattern of the
// shape `pkg.Ctor(args)` — e.g. `case acp.Acked()` or `case acp.OutcomeResult(x)`.
// In the grammar this is a postfix expression with primary `pkg` and two
// suffixes: a member access `.Ctor` followed by a call `(...)`. The unqualified
// helper (getCallPatternWithTypeArgsFromExpression) treats a two-suffix postfix
// as `Ctor[T](...)`, so it does not recognize this shape and the pattern would
// otherwise fall through to a simple binding of `pkg`.
//
// Returns the primary expr for the package qualifier, the constructor name, the
// argument list (nil for an empty call), and ok=true when the shape matches.
func (t *galaASTTransformer) getQualifiedCallPattern(ctx grammar.IExpressionContext) (pkgPrimaryExpr *grammar.PrimaryExprContext, ctorName string, argList *grammar.ArgumentListContext, typeArgs *grammar.ExpressionListContext, ok bool) {
	postfixCtx := t.getSinglePostfixExpr(ctx)
	if postfixCtx == nil {
		return nil, "", nil, nil, false
	}

	// `pkg.Ctor(args)`, or `pkg.Ctor[T](args)` with explicit type arguments.
	suffixes := postfixCtx.AllPostfixSuffix()
	if len(suffixes) != 2 && len(suffixes) != 3 {
		return nil, "", nil, nil, false
	}
	memberSuffix := suffixes[0].(*grammar.PostfixSuffixContext)
	callSuffix := suffixes[len(suffixes)-1].(*grammar.PostfixSuffixContext)

	// First suffix must be a member access `.Ident`.
	if memberSuffix.Identifier() == nil {
		return nil, "", nil, nil, false
	}
	ctorName = memberSuffix.Identifier().GetText()

	// A middle suffix must be type arguments `[...]`.
	if len(suffixes) == 3 {
		typeArgsSuffix := suffixes[1].(*grammar.PostfixSuffixContext)
		if suffixOpener(typeArgsSuffix) != "[" {
			return nil, "", nil, nil, false
		}
		if el := typeArgsSuffix.ExpressionList(); el != nil {
			typeArgs = el.(*grammar.ExpressionListContext)
		}
	}

	// Last suffix must be a call `(...)`.
	if callSuffix.GetChildCount() < 2 || suffixOpener(callSuffix) != "(" {
		return nil, "", nil, nil, false
	}

	primaryExpr := postfixCtx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, "", nil, nil, false
	}
	if al := callSuffix.ArgumentList(); al != nil {
		argList = al.(*grammar.ArgumentListContext)
	}
	return primaryExpr.(*grammar.PrimaryExprContext), ctorName, argList, typeArgs, true
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
	default:
		return token.ILLEGAL
	}
}

// transformPrimary, transformCompositeLiteral, transformLiteral moved to constructors.go
// Lambda-related functions moved to lambdas.go
// findLambdaInExpression moved to lambdas.go
func (t *galaASTTransformer) transformIfExpression(ctx *grammar.IfExpressionContext) (ast.Expr, error) {
	return t.transformIfExpressionAgainst(ctx, slot{})
}

// transformIfExpressionAgainst lowers an if-expression to an IIFE. s is the slot
// it fills (zero when none): each branch value is lowered against it, and its
// type informs the IIFE's result type (see branchingResultType).
func (t *galaASTTransformer) transformIfExpressionAgainst(ctx *grammar.IfExpressionContext, s slot) (ast.Expr, error) {
	// 'if' '(' cond ')' thenBranch 'else' elseBranch
	// Branches can be either expressions or blocks.
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	// The if-expression lowers to an IIFE: a `return` in a branch yields the
	// branch's value, not the enclosing lambda's. An open slot type holds
	// placeholders, so it is no return type.
	branches := ctx.AllIfExprBranch()
	// An if-expression whose value a local declaration stores, and whose
	// branches hold a `return`, `break` or `continue`, is lowered as
	// statements storing its value (see hoisted_value.go): its control flow
	// acts on the enclosing function or loop.
	hoist := s.hoist
	if hoist != nil && !slices.ContainsFunc(branches, func(b grammar.IIfExprBranchContext) bool { return escapesConstruct(b) }) {
		hoist = nil
	}
	s.hoist = hoist
	if hoist == nil {
		iifeType := s.typ
		if s.open {
			iifeType = nil
		}
		defer t.enterIIFEReturnSlot(iifeType)()
	}

	// A branch with no slot type to lower against is typed by the other one
	// (see lowerBranches).
	var lowered [2]struct {
		stmts      []ast.Stmt
		expr       ast.Expr
		terminates bool
	}
	siblingTyped := transpiler.IsUnusable(s.typ)
	lowerBranch := func(i int, bs slot) (transpiler.Type, error) {
		b := &lowered[i]
		var err error
		if b.stmts, b.expr, b.terminates, err = t.transformIfExprBranch(branches[i].(*grammar.IfExprBranchContext), bs); err != nil || !siblingTyped {
			return nil, err
		}
		if hoist != nil && b.terminates {
			// The branch leaves the function or loop: it has no value.
			return nil, nil
		}
		return t.getExprTypeName(b.expr), nil
	}
	if err := t.lowerBranches(2, s, siblingTyped, lowerBranch); err != nil {
		return nil, err
	}
	if hoist != nil {
		return t.hoistIfExpression(ctx, cond, s, lowered[0].stmts, lowered[0].expr, lowered[0].terminates,
			lowered[1].stmts, lowered[1].expr, lowered[1].terminates)
	}
	if err := t.checkNoLoopControlInValue("an if-expression", lowered[0].stmts, lowered[1].stmts); err != nil {
		return nil, err
	}
	thenStmts, thenExpr, thenTerminates := lowered[0].stmts, lowered[0].expr, lowered[0].terminates
	elseStmts, elseExpr, elseTerminates := lowered[1].stmts, lowered[1].expr, lowered[1].terminates

	retType := transpiler.Type(transpiler.NilType{})
	if inferred, err := t.inferIfType(cond, thenExpr, elseExpr); err == nil && !inferred.IsNil() {
		retType = inferred
	}

	// HM-based inference cannot model methods on user-defined generic types
	// (the type env only carries top-level functions and current-scope vals),
	// so an if-expression like `if (x.IsDue()) x.Advance() else x` falls
	// through with retType=NilType. Before defaulting to `any`, try per-branch
	// inference via getExprTypeName and unify — this is the same machinery
	// used by block-bodied lambdas (unifyBlockReturnTypes) and reliably
	// resolves the common arm type even when both arms are user methods.
	if retType.IsNil() {
		thenT := t.getExprTypeName(thenExpr)
		elseT := t.getExprTypeName(elseExpr)
		if !thenT.IsNil() && !thenT.IsAny() && !elseT.IsNil() && !elseT.IsAny() {
			if thenT.String() == elseT.String() {
				retType = thenT
			} else if unified := t.pickMoreSpecificType(thenT, elseT); unified != nil {
				retType = unified
			}
		} else if !thenT.IsNil() && !thenT.IsAny() {
			retType = thenT
		} else if !elseT.IsNil() && !elseT.IsAny() {
			retType = elseT
		}
	}

	// Neither branch has a type, and nothing the if-expression fills gives it
	// one. When every branch produces no value (`{}`, a trailing statement or
	// a void call) it runs like the void case below; any other untyped value
	// has no type the closure could return.
	if transpiler.IsUnusable(t.branchingResultType(retType, s)) {
		for i, b := range branches {
			if !ifBranchHasNoValue(b.(*grammar.IfExprBranchContext)) && !t.getExprTypeName(lowered[i].expr).IsVoid() {
				return nil, galaerr.NewCodedSemanticError(
					galaerr.CodeUntypedBranchingValue,
					ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
					"cannot infer the type of this if-expression: no branch has a known type",
					"declare the type its value fills (e.g. `val x Option[int] = if (...) ...`) or give a branch a typed value (e.g. `None[int]()`)")
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
		branchBody := func(stmts []ast.Stmt, last ast.Expr, terminates bool) *ast.BlockStmt {
			if !terminates && last != nil && !isNilIdent(last) {
				stmts = append(stmts, &ast.ExprStmt{X: last})
			}
			return &ast.BlockStmt{List: stmts}
		}
		call := &ast.CallExpr{
			Fun: &ast.FuncLit{
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.IfStmt{
					Cond: cond,
					Body: branchBody(thenStmts, thenExpr, thenTerminates),
					Else: branchBody(elseStmts, elseExpr, elseTerminates),
				}}},
			},
		}
		t.recordBranchingCall(call, "if-expression", true, ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
		return call, nil
	}

	retTypeExpr := t.typeToExpr(t.branchingResultType(retType, s))

	// Build the then-block: preceding statements + return lastExpr.
	// When the branch already terminates with an explicit return, the synthesized
	// return is unreachable dead code (and would be `return nil` since the branch
	// expression is a placeholder), so omit it.
	thenBody := thenStmts
	if !thenTerminates {
		thenBody = append(thenBody, &ast.ReturnStmt{Results: []ast.Expr{thenExpr}})
	}

	// Build the else-block: preceding statements + return lastExpr (see above).
	elseBody := elseStmts
	if !elseTerminates {
		elseBody = append(elseBody, &ast.ReturnStmt{Results: []ast.Expr{elseExpr}})
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
						Body: &ast.BlockStmt{List: thenBody},
						Else: &ast.BlockStmt{List: elseBody},
					},
				},
			},
		},
	}
	t.recordBranchingCall(call, "if-expression", false, ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
	return call, nil
}

// hoistIfExpression lowers an if-expression whose value s.hoist stores, from
// its lowered branches, as an if statement storing each branch's value: a
// branch that terminates (ends in a `return`, `break` or `continue`) stores
// none. The value's type is that of the branches that store one.
func (t *galaASTTransformer) hoistIfExpression(ctx *grammar.IfExpressionContext, cond ast.Expr, s slot,
	thenStmts []ast.Stmt, thenExpr ast.Expr, thenTerminates bool,
	elseStmts []ast.Stmt, elseExpr ast.Expr, elseTerminates bool) (ast.Expr, error) {
	var typ transpiler.Type = transpiler.NilType{}
	switch {
	case !thenTerminates && !elseTerminates:
		thenT, elseT := t.getExprTypeName(thenExpr), t.getExprTypeName(elseExpr)
		if thenT.String() == elseT.String() {
			typ = thenT
		} else if unified := t.pickMoreSpecificType(thenT, elseT); unified != nil {
			typ = unified
		}
	case !thenTerminates:
		typ = t.getExprTypeName(thenExpr)
	case !elseTerminates:
		typ = t.getExprTypeName(elseExpr)
	}
	typ = t.branchingResultType(typ, s)
	if transpiler.IsUnusable(typ) || typ.IsVoid() {
		return nil, untypedBranchingError("if-expression", ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
	}
	branch := func(stmts []ast.Stmt, value ast.Expr, terminates bool) *ast.BlockStmt {
		if !terminates {
			stmts = append(stmts, t.storeValue(value, s.hoist)...)
		}
		return &ast.BlockStmt{List: stmts}
	}
	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: branch(thenStmts, thenExpr, thenTerminates),
		Else: branch(elseStmts, elseExpr, elseTerminates),
	}
	return t.hoistedResult(s.hoist, []ast.Stmt{ifStmt}, typ), nil
}

// expressionIsBareMatch reports whether the expression context is a bare
// `subject match { ... }` form whose value is the entire expression (i.e.
// not nested inside arithmetic, calls, or other operators). When such a
// match appears in statement position, its value is discarded — see
// transformBlock, which uses this to mark the match as statement-position
// so void-returning arm calls do not get wrapped in `return ...`.
func (t *galaASTTransformer) expressionIsBareMatch(exprCtx grammar.IExpressionContext) bool {
	return t.bareMatchPostfix(exprCtx) != nil
}

// bareMatchPostfix returns the postfix expression of a bare match (see
// expressionIsBareMatch), or nil.
func (t *galaASTTransformer) bareMatchPostfix(exprCtx grammar.IExpressionContext) *grammar.PostfixExprContext {
	postfixCtx := t.barePostfix(exprCtx)
	if postfixCtx == nil || postfixCtx.GetChildCount() <= 1 {
		return nil
	}
	// transformPostfixExpr detects match by scanning children for a node whose
	// text is the keyword `match`. Mirror that here.
	for i := 0; i < postfixCtx.GetChildCount(); i++ {
		child := postfixCtx.GetChild(i)
		if child == nil {
			continue
		}
		if pt, ok := child.(antlr.ParseTree); ok && pt.GetText() == "match" {
			return postfixCtx
		}
	}
	return nil
}

// barePostfix returns the postfix expression exprCtx consists of when no
// operator surrounds it, or nil.
func (t *galaASTTransformer) barePostfix(exprCtx grammar.IExpressionContext) *grammar.PostfixExprContext {
	if exprCtx == nil {
		return nil
	}
	orExpr := exprCtx.OrExpr()
	if orExpr == nil {
		return nil
	}
	orCtx := orExpr.(*grammar.OrExprContext)
	if len(orCtx.AllAndExpr()) != 1 {
		return nil
	}
	andCtx := orCtx.AndExpr(0).(*grammar.AndExprContext)
	if len(andCtx.AllEqualityExpr()) != 1 {
		return nil
	}
	eqCtx := andCtx.EqualityExpr(0).(*grammar.EqualityExprContext)
	if len(eqCtx.AllRelationalExpr()) != 1 {
		return nil
	}
	relCtx := eqCtx.RelationalExpr(0).(*grammar.RelationalExprContext)
	if len(relCtx.AllAdditiveExpr()) != 1 {
		return nil
	}
	addCtx := relCtx.AdditiveExpr(0).(*grammar.AdditiveExprContext)
	if len(addCtx.AllMultiplicativeExpr()) != 1 {
		return nil
	}
	mulCtx := addCtx.MultiplicativeExpr(0).(*grammar.MultiplicativeExprContext)
	if len(mulCtx.AllUnaryExpr()) != 1 {
		return nil
	}
	unaryCtx := mulCtx.UnaryExpr(0).(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil
	}
	return postfixExpr.(*grammar.PostfixExprContext)
}

// findIfExpressionInExpression traverses the expression tree to find an if-expression
// if the expression is simply an if-expression (not part of a larger expression).
// Follows the same traversal pattern as findLambdaInExpression in lambdas.go.
func (t *galaASTTransformer) findIfExpressionInExpression(exprCtx grammar.IExpressionContext) *grammar.IfExpressionContext {
	if exprCtx == nil {
		return nil
	}
	orExpr := exprCtx.OrExpr()
	if orExpr == nil {
		return nil
	}
	orCtx := orExpr.(*grammar.OrExprContext)
	if len(orCtx.AllAndExpr()) != 1 {
		return nil
	}
	andCtx := orCtx.AndExpr(0).(*grammar.AndExprContext)
	if len(andCtx.AllEqualityExpr()) != 1 {
		return nil
	}
	eqCtx := andCtx.EqualityExpr(0).(*grammar.EqualityExprContext)
	if len(eqCtx.AllRelationalExpr()) != 1 {
		return nil
	}
	relCtx := eqCtx.RelationalExpr(0).(*grammar.RelationalExprContext)
	if len(relCtx.AllAdditiveExpr()) != 1 {
		return nil
	}
	addCtx := relCtx.AdditiveExpr(0).(*grammar.AdditiveExprContext)
	if len(addCtx.AllMultiplicativeExpr()) != 1 {
		return nil
	}
	mulCtx := addCtx.MultiplicativeExpr(0).(*grammar.MultiplicativeExprContext)
	if len(mulCtx.AllUnaryExpr()) != 1 {
		return nil
	}
	unaryCtx := mulCtx.UnaryExpr(0).(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil
	}
	postfixCtx := postfixExpr.(*grammar.PostfixExprContext)
	if len(postfixCtx.AllPostfixSuffix()) > 0 {
		return nil
	}
	primExpr := postfixCtx.PrimaryExpr()
	if primExpr == nil {
		return nil
	}
	primCtx := primExpr.(*grammar.PrimaryExprContext)
	ifExpr := primCtx.IfExpression()
	if ifExpr == nil {
		return nil
	}
	return ifExpr.(*grammar.IfExpressionContext)
}

// ifBranchHasNoValue reports whether an if-expression branch produces no
// value: an empty block, or a block that ends in a statement other than an
// expression or a `return` (transformIfExprBranch gives it the placeholder
// value `nil`).
func ifBranchHasNoValue(ctx *grammar.IfExprBranchContext) bool {
	blockCtx, ok := ctx.Block().(*grammar.BlockContext)
	if !ok || blockCtx == nil {
		return false
	}
	stmts := blockCtx.AllStatement()
	if len(stmts) == 0 {
		return true
	}
	last := stmts[len(stmts)-1].(*grammar.StatementContext)
	return trailingValueExpression(last) == nil && last.ReturnStatement() == nil
}

// isNilIdent reports whether expr is the identifier `nil`.
func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// transformIfExprBranch transforms an if-expression branch, which can be
// either a single expression or a block. For blocks, the last statement
// must be an expression statement — it becomes the branch's return value,
// and preceding statements are executed before it.
//
// The third return value reports whether the branch already terminates
// (its last statement is an explicit `return`); when true, the caller must
// not append a synthesized `return <expr>` to the branch body, since that
// would be unreachable dead code with a placeholder expression.
//
// s is the if-expression's slot; the branch's value expression is lowered
// against it (see lowerAgainst).
func (t *galaASTTransformer) transformIfExprBranch(ctx *grammar.IfExprBranchContext, s slot) ([]ast.Stmt, ast.Expr, bool, error) {
	if exprCtx := ctx.Expression(); exprCtx != nil {
		if bs, ok := t.lowerLoopControl(exprCtx); ok {
			if s.hoist != nil {
				// Lowered as statements, the branch acts on the loop: it
				// terminates, with no value.
				return []ast.Stmt{bs}, nil, true, nil
			}
			return nil, nil, false, t.loopControlInValueError("an if-expression", bs)
		}
		expr, err := t.lowerAgainst(exprCtx, s, true)
		if err != nil {
			return nil, nil, false, err
		}
		return nil, expr, false, nil
	}

	// Block branch: transform all statements, use last as the result expression.
	// Path: statement → declaration → simpleStatement → expression
	blockCtx := ctx.Block().(*grammar.BlockContext)
	stmts := blockCtx.AllStatement()
	if len(stmts) == 0 {
		return nil, ast.NewIdent("nil"), false, nil
	}

	var preceding []ast.Stmt
	for _, stmtCtx := range stmts[:len(stmts)-1] {
		stmt, err := t.transformStatement(stmtCtx.(*grammar.StatementContext))
		if err != nil {
			return nil, nil, false, err
		}
		preceding = t.spliceStmt(preceding, stmt)
	}

	// Try to extract expression from the last statement:
	// statement → declaration → simpleStatement → expression
	lastStmtCtx := stmts[len(stmts)-1].(*grammar.StatementContext)
	if exprCtx := trailingValueExpression(lastStmtCtx); exprCtx != nil {
		// The branch's value is its trailing expression; loop control has none.
		if bs, ok := t.lowerLoopControl(exprCtx); ok {
			if s.hoist != nil {
				return append(preceding, bs), nil, true, nil
			}
			return nil, nil, false, t.loopControlInValueError("an if-expression", bs)
		}
		expr, err := t.lowerAgainst(exprCtx, s, true)
		if err != nil {
			return nil, nil, false, err
		}
		return preceding, expr, false, nil
	}

	// If the last statement isn't a bare expression, transform it normally
	// and return nil as the expression (void block).
	lastStmt, err := t.transformStatement(lastStmtCtx)
	if err != nil {
		return nil, nil, false, err
	}
	preceding = t.spliceStmt(preceding, lastStmt)
	// If the last statement is an explicit return, the branch terminates: the
	// caller must skip its synthesized trailing return so we don't emit dead
	// `return nil` after a typed return. We also surface the return's value
	// expression as the branch expression so the IIFE result type can be
	// inferred from it (rather than defaulting to `any`).
	if retStmt, ok := lastStmt.(*ast.ReturnStmt); ok && lastStmtCtx.ReturnStatement() != nil {
		var branchExpr ast.Expr = ast.NewIdent("nil")
		if len(retStmt.Results) > 0 {
			branchExpr = retStmt.Results[0]
		}
		return preceding, branchExpr, true, nil
	}
	return preceding, ast.NewIdent("nil"), false, nil
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
	hoist *hoistTarget
}

// typedSlot is the slot of type typ: an argument, a declaration, a function
// or lambda result. Each gives typ to the value that fills it, and only to it
// (see consumesSlotType).
func typedSlot(typ transpiler.Type) slot { return slot{typ: typ} }

// exprForm classifies an expression by the forms whose lowering depends on
// the slot they fill; at most one field is set.
type exprForm struct {
	grouped grammar.IExpressionContext // e for `(e)`
	lambda  *grammar.LambdaExpressionContext
	ifExpr  *grammar.IfExpressionContext
	match   *grammar.PostfixExprContext
}

func (t *galaASTTransformer) classifyExpr(exprCtx grammar.IExpressionContext) exprForm {
	if exprCtx == nil {
		return exprForm{}
	}
	if inner := t.groupedExpression(exprCtx); inner != nil {
		return exprForm{grouped: inner}
	}
	if l := t.findLambdaInExpression(exprCtx); l != nil {
		return exprForm{lambda: l}
	}
	if i := t.findIfExpressionInExpression(exprCtx); i != nil {
		return exprForm{ifExpr: i}
	}
	return exprForm{match: t.bareMatchPostfix(exprCtx)}
}

// needsExpectedType reports whether exprCtx is a lambda, an if-expression or a
// match, possibly parenthesized.
func (t *galaASTTransformer) needsExpectedType(exprCtx grammar.IExpressionContext) bool {
	f := t.classifyExpr(exprCtx)
	if f.grouped != nil {
		return t.needsExpectedType(f.grouped)
	}
	return f.lambda != nil || f.ifExpr != nil || f.match != nil
}

// lowerAgainst lowers exprCtx in check mode against the slot it fills. A lambda
// takes its parameter and result types from a function type (strict is
// transformLambdaWithExpectedType's untyped-parameter policy); an if-expression
// or match lowers each branch against the same slot (branch lambdas strictly);
// a plain expression sees the slot type only if it consumes it
// (consumesSlotType). A lambda body is never lowered against the outer slot:
// it gets typedSlot(the lambda's result type).
func (t *galaASTTransformer) lowerAgainst(exprCtx grammar.IExpressionContext, s slot, strict bool) (ast.Expr, error) {
	if transpiler.IsUnusable(s.typ) && (s.hoist == nil || !t.hoistable(exprCtx)) {
		return t.transformExpression(exprCtx)
	}
	f := t.classifyExpr(exprCtx)
	switch {
	case f.grouped != nil:
		expr, err := t.lowerAgainst(f.grouped, s, strict)
		if err != nil {
			return nil, err
		}
		return &ast.ParenExpr{X: expr}, nil
	case f.lambda != nil:
		if expectedRetType, expectedParamTypes, ok := t.lambdaExpectation(s.typ); ok {
			return t.transformLambdaWithExpectedType(f.lambda, expectedRetType, expectedParamTypes, strict)
		}
		return t.transformExpression(exprCtx)
	case f.ifExpr != nil:
		return t.transformIfExpressionAgainst(f.ifExpr, s)
	case f.match != nil:
		return t.transformPostfixMatchExpressionAgainst(f.match, s)
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
	if hint := t.followAliasChain(s.typ); t.consumesSlotType(exprCtx, hint) {
		release := t.expectedArgTypes.pushFor(hint, t.barePostfix(exprCtx))
		defer release()
	}
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}
	// A Go call converted to a Try or Tuple where its plain value is expected
	// is named here, not left to Go's type mismatch on the generated code.
	if !s.open && !s.tryThunk {
		if err := t.checkGoResultAgainst(expr, s.typ, exprCtx); err != nil {
			return nil, err
		}
	}
	// An opaque type and its underlying type (or another opaque type) never
	// convert implicitly; say so in GALA rather than leave it to Go. An open
	// slot is not a type the value has to have.
	if !s.open {
		if err := t.checkOpaqueMismatch(expr, s.typ, exprCtx); err != nil {
			return nil, err
		}
	}
	return expr, nil
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
	case transpiler.PointerType:
		return typeHasMaskedPart(v.Elem)
	case transpiler.MapType:
		return typeHasMaskedPart(v.Key) || typeHasMaskedPart(v.Elem)
	}
	return false
}

// groupedExpression returns e for an expression that is exactly `(e)`, or nil.
func (t *galaASTTransformer) groupedExpression(exprCtx grammar.IExpressionContext) grammar.IExpressionContext {
	list := t.parenthesizedList(exprCtx)
	if list == nil || len(list.AllExpression()) != 1 {
		return nil
	}
	return list.Expression(0)
}

// consumesSlotType reports whether exprCtx is a plain expression that takes
// the type of the slot it fills (see lowerAgainst) — an argument, a
// declaration, a function or lambda result — given as hint, the type an alias
// names: a tuple literal of that tuple type, a construction of a value of
// that generic type (isConstructionOf), a call of a generic function whose
// result-only type parameters only the slot can give (isPhantomGenericCall),
// or a call of a method whose own type parameters its result mentions
// (isResultGenericMethodCall).
//
// This is the one place a slot type reaches a value's type arguments, and it
// reaches only the call or literal that is the value: never a receiver it is
// applied to (see expectedArgTypeStack.withhold), an argument nested in it,
// or a value elsewhere in the enclosing function.
func (t *galaASTTransformer) consumesSlotType(exprCtx grammar.IExpressionContext, hint transpiler.Type) bool {
	if t.isTupleLiteralFor(exprCtx, hint) {
		return true
	}
	name, typeArgs := t.calleeOfCall(exprCtx)
	if name != "" && (t.isConstructionOf(name, hint) || !typeArgs && t.isPhantomGenericCall(name)) {
		return true
	}
	return t.isResultGenericMethodCall(exprCtx)
}

// isResultGenericMethodCall reports whether exprCtx is a method call written
// without type arguments, `recv.m(...)`, where some type declares a method m
// whose own type parameters its result mentions (`Convert[U any]() Option[U]`,
// `Map[U any](f func(T) U) Option[U]`): the slot the call fills can bind
// them. The receiver's type is not known before it is lowered, so any such m
// qualifies; the method call itself takes the slot type only when it is one.
func (t *galaASTTransformer) isResultGenericMethodCall(exprCtx grammar.IExpressionContext) bool {
	p := t.barePostfix(exprCtx)
	if p == nil || len(p.AllCaseClause()) > 0 {
		return false
	}
	suffixes := p.AllPostfixSuffix()
	if len(suffixes) == 0 || !isCallSuffix(suffixes[len(suffixes)-1]) {
		return false
	}
	member := calledMemberToken(suffixes[len(suffixes)-1])
	return member != nil && t.resultGenericMethods[member.GetText()]
}

// isTupleLiteralFor reports whether exprCtx is exactly a tuple literal
// `(a, b, ...)` whose arity matches the tuple type typ.
func (t *galaASTTransformer) isTupleLiteralFor(exprCtx grammar.IExpressionContext, typ transpiler.Type) bool {
	gen, ok := typ.(transpiler.GenericType)
	if !ok || !t.isTupleTypeName(gen.Base.String()) {
		return false
	}
	list := t.parenthesizedList(exprCtx)
	return list != nil && len(list.AllExpression()) > 1 && len(list.AllExpression()) == len(gen.Params)
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
// whether it has any; "" for anything else.
func (t *galaASTTransformer) calleeOfCall(exprCtx grammar.IExpressionContext) (name string, typeArgs bool) {
	if p := t.barePostfix(exprCtx); p == nil || len(p.AllCaseClause()) > 0 {
		return "", false
	}
	if prim, _, args := t.getCallPatternWithTypeArgsFromExpression(exprCtx); prim != nil {
		return prim.GetText(), args != nil
	}
	if pkg, ctor, _, args, ok := t.getQualifiedCallPattern(exprCtx); ok {
		return pkg.GetText() + "." + ctor, args != nil
	}
	return "", false
}

// isPhantomGenericCall reports whether name (see calleeOfCall, called without
// type arguments) is a generic function with a type parameter only its result
// mentions (`parse()` for `func parse[T any]() Option[T]`): the slot the call
// fills is the only place that parameter can come from.
func (t *galaASTTransformer) isPhantomGenericCall(name string) bool {
	meta := t.getFunction(name)
	if meta == nil {
		return false
	}
	_, phantom := t.phantomTypeParams(meta.TypeParams, meta.ParamTypes)
	return len(phantom) > 0
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

// isCallSuffix reports whether s is an argument list `(...)`.
func isCallSuffix(s grammar.IPostfixSuffixContext) bool {
	sc := s.(*grammar.PostfixSuffixContext)
	return sc.Identifier() == nil && sc.ExpressionList() == nil
}

// parenthesizedList returns the list of an expression that is exactly a
// parenthesized expression list — `(e)` or a tuple literal — or nil.
func (t *galaASTTransformer) parenthesizedList(exprCtx grammar.IExpressionContext) *grammar.TupleExpressionListContext {
	p := t.barePostfix(exprCtx)
	if p == nil || p.GetChildCount() != 1 {
		return nil
	}
	primExpr, ok := p.PrimaryExpr().(*grammar.PrimaryExprContext)
	if !ok || primExpr == nil {
		return nil
	}
	prim, ok := primExpr.Primary().(*grammar.PrimaryContext)
	if !ok || prim == nil {
		return nil
	}
	list, ok := prim.TupleExpressionList().(*grammar.TupleExpressionListContext)
	if !ok {
		return nil
	}
	return list
}

// unwrapImmutable is the single canonical unwrap helper for val-wrapped
// Immutable[T] values. Given an expression of type Immutable[T], it returns
// a .Get() call to produce the underlying T; otherwise it returns the
// expression unchanged (pure type names and non-Immutable values are
// pass-through). All transformer paths that need to read through an
// Immutable wrapper MUST route through this function — do not re-implement
// the logic inline. See also unwrapConstPtr for ConstPtr[T] dereference.
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
			// cache, so getExprTypeName re-runs manual inference and then falls
			// through to Hindley-Milner on every such query.
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

// unwrapConstPtr dereferences a ConstPtr to access its underlying value.
// This is used when accessing fields on a ConstPtr[T] - we need to call Deref() to get T.
func (t *galaASTTransformer) unwrapConstPtr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	typeObj := t.getExprTypeName(expr)
	if t.isConstPtrType(typeObj) {
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   expr,
				Sel: ast.NewIdent(transpiler.MethodDeref),
			},
		}
	}
	return expr
}

// transformTupleLiteral transforms (a, b) to std.Tuple{V1: NewImmutable(a), V2: NewImmutable(b)},
// (a, b, c) to std.Tuple3{V1: NewImmutable(a), V2: NewImmutable(b), V3: NewImmutable(c)}, etc.
// transformTupleLiteral moved to postfix.go
