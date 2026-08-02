package transformer

import (
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
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
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: token.LOR, Y: right}
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
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: token.LAND, Y: right}
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
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
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
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
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
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
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
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
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
	name := primaryCtx.Identifier().GetText()
	if t.isVal(name) {
		return name
	}
	return ""
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
	if ctx == nil {
		return nil
	}
	// expression -> orExpr
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return nil
	}
	// orExpr -> andExpr
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) == 0 {
		return nil
	}
	// andExpr -> equalityExpr
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) == 0 {
		return nil
	}
	// equalityExpr -> relationalExpr
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) == 0 {
		return nil
	}
	// relationalExpr -> additiveExpr
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) == 0 {
		return nil
	}
	// additiveExpr -> multiplicativeExpr
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) == 0 {
		return nil
	}
	// multiplicativeExpr -> unaryExpr
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) == 0 {
		return nil
	}
	// unaryExpr -> postfixExpr (if no unaryOp)
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil
	}
	// postfixExpr -> primaryExpr
	primaryExpr := postfixExpr.(*grammar.PostfixExprContext).PrimaryExpr()
	if primaryExpr == nil {
		return nil
	}
	// primaryExpr -> primary. The primaryExpr alternation also covers
	// lambdaExpression, ifExpression, and partialFunctionLiteral; for those
	// shapes Primary() returns a typed-nil interface and the cast below would
	// panic. Callers all check for nil already, so just bail out.
	primary := primaryExpr.(*grammar.PrimaryExprContext).Primary()
	if primary == nil {
		return nil
	}
	return primary.(*grammar.PrimaryContext)
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
func (t *galaASTTransformer) getQualifiedCallPattern(ctx grammar.IExpressionContext) (pkgPrimaryExpr *grammar.PrimaryExprContext, ctorName string, argList *grammar.ArgumentListContext, ok bool) {
	postfixCtx := t.getSinglePostfixExpr(ctx)
	if postfixCtx == nil {
		return nil, "", nil, false
	}

	suffixes := postfixCtx.AllPostfixSuffix()
	if len(suffixes) != 2 {
		return nil, "", nil, false
	}
	memberSuffix := suffixes[0].(*grammar.PostfixSuffixContext)
	callSuffix := suffixes[1].(*grammar.PostfixSuffixContext)

	// First suffix must be a member access `.Ident`.
	if memberSuffix.Identifier() == nil {
		return nil, "", nil, false
	}
	ctorName = memberSuffix.Identifier().GetText()

	// Second suffix must be a call `(...)`.
	if callSuffix.GetChildCount() < 2 {
		return nil, "", nil, false
	}
	if callSuffix.GetChild(0).(antlr.ParseTree).GetText() != "(" {
		return nil, "", nil, false
	}

	primaryExpr := postfixCtx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, "", nil, false
	}
	if al := callSuffix.ArgumentList(); al != nil {
		argList = al.(*grammar.ArgumentListContext)
	}
	return primaryExpr.(*grammar.PrimaryExprContext), ctorName, argList, true
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
	// 'if' '(' cond ')' thenBranch 'else' elseBranch
	// Branches can be either expressions or blocks.
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}

	// An if-expression feeding a function-typed slot (e.g.
	// `val f func(int) int = if (c) { (x) => x } else { (x) => x * 2 }`) threads
	// the declared signature into each branch's lambda. The bare lambda consumes
	// the hint (transformLambda), so re-establish it before each branch.
	branchParams, branchRet := t.expectedLambdaParamTypes, t.expectedLambdaRetType

	branches := ctx.AllIfExprBranch()
	t.expectedLambdaParamTypes, t.expectedLambdaRetType = branchParams, branchRet
	thenStmts, thenExpr, thenTerminates, err := t.transformIfExprBranch(branches[0].(*grammar.IfExprBranchContext))
	if err != nil {
		return nil, err
	}
	t.expectedLambdaParamTypes, t.expectedLambdaRetType = branchParams, branchRet
	elseStmts, elseExpr, elseTerminates, err := t.transformIfExprBranch(branches[1].(*grammar.IfExprBranchContext))
	if err != nil {
		return nil, err
	}
	t.expectedLambdaParamTypes, t.expectedLambdaRetType = branchParams, branchRet

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

	retTypeExpr := t.typeToExpr(retType)
	// If type inference failed and we have an expected type from the enclosing function, use it
	if retType.IsNil() && t.expectedIfExprType != nil {
		retTypeExpr = t.expectedIfExprType
	}

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
	return &ast.CallExpr{
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
	}, nil
}

// expressionIsBareMatch reports whether the expression context is a bare
// `subject match { ... }` form whose value is the entire expression (i.e.
// not nested inside arithmetic, calls, or other operators). When such a
// match appears in statement position, its value is discarded — see
// transformBlock, which uses this to mark the match as statement-position
// so void-returning arm calls do not get wrapped in `return ...`.
func (t *galaASTTransformer) expressionIsBareMatch(exprCtx grammar.IExpressionContext) bool {
	if exprCtx == nil {
		return false
	}
	orExpr := exprCtx.OrExpr()
	if orExpr == nil {
		return false
	}
	orCtx := orExpr.(*grammar.OrExprContext)
	if len(orCtx.AllAndExpr()) != 1 {
		return false
	}
	andCtx := orCtx.AndExpr(0).(*grammar.AndExprContext)
	if len(andCtx.AllEqualityExpr()) != 1 {
		return false
	}
	eqCtx := andCtx.EqualityExpr(0).(*grammar.EqualityExprContext)
	if len(eqCtx.AllRelationalExpr()) != 1 {
		return false
	}
	relCtx := eqCtx.RelationalExpr(0).(*grammar.RelationalExprContext)
	if len(relCtx.AllAdditiveExpr()) != 1 {
		return false
	}
	addCtx := relCtx.AdditiveExpr(0).(*grammar.AdditiveExprContext)
	if len(addCtx.AllMultiplicativeExpr()) != 1 {
		return false
	}
	mulCtx := addCtx.MultiplicativeExpr(0).(*grammar.MultiplicativeExprContext)
	if len(mulCtx.AllUnaryExpr()) != 1 {
		return false
	}
	unaryCtx := mulCtx.UnaryExpr(0).(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return false
	}
	postfixCtx := postfixExpr.(*grammar.PostfixExprContext)
	// transformPostfixExpr detects match by scanning children for a node whose
	// text is the keyword `match`. Mirror that here.
	if postfixCtx.GetChildCount() <= 1 {
		return false
	}
	for i := 0; i < postfixCtx.GetChildCount(); i++ {
		child := postfixCtx.GetChild(i)
		if child == nil {
			continue
		}
		if pt, ok := child.(antlr.ParseTree); ok && pt.GetText() == "match" {
			return true
		}
	}
	return false
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

// transformIfExprBranch transforms an if-expression branch, which can be
// either a single expression or a block. For blocks, the last statement
// must be an expression statement — it becomes the branch's return value,
// and preceding statements are executed before it.
//
// The third return value reports whether the branch already terminates
// (its last statement is an explicit `return`); when true, the caller must
// not append a synthesized `return <expr>` to the branch body, since that
// would be unreachable dead code with a placeholder expression.
func (t *galaASTTransformer) transformIfExprBranch(ctx *grammar.IfExprBranchContext) ([]ast.Stmt, ast.Expr, bool, error) {
	if exprCtx := ctx.Expression(); exprCtx != nil {
		expr, err := t.transformExpression(exprCtx)
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
		preceding = append(preceding, stmt)
	}

	// Try to extract expression from the last statement:
	// statement → declaration → simpleStatement → expression
	lastStmtCtx := stmts[len(stmts)-1].(*grammar.StatementContext)
	if declCtx := lastStmtCtx.Declaration(); declCtx != nil {
		if simpleCtx := declCtx.SimpleStatement(); simpleCtx != nil {
			if exprCtx := simpleCtx.Expression(); exprCtx != nil {
				expr, err := t.transformExpression(exprCtx)
				if err != nil {
					return nil, nil, false, err
				}
				return preceding, expr, false, nil
			}
		}
	}

	// If the last statement isn't a bare expression, transform it normally
	// and return nil as the expression (void block).
	lastStmt, err := t.transformStatement(lastStmtCtx)
	if err != nil {
		return nil, nil, false, err
	}
	preceding = append(preceding, lastStmt)
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
		if !t.isVal(ident.Name) && !t.isVar(ident.Name) {
			if !t.getType(ident.Name).IsNil() {
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
				if !t.getType(fullPath).IsNil() {
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
