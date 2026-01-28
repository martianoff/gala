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

	// With the new grammar, expression simply wraps orExpr
	if orExpr := ctx.OrExpr(); orExpr != nil {
		return t.transformOrExpr(orExpr.(*grammar.OrExprContext))
	}

	return nil, galaerr.NewSemanticError("expression must contain orExpr")
}

func (t *galaASTTransformer) transformOrExpr(ctx *grammar.OrExprContext) (ast.Expr, error) {
	andExprs := ctx.AllAndExpr()
	if len(andExprs) == 0 {
		return nil, galaerr.NewSemanticError("orExpr must have at least one andExpr")
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
		return nil, galaerr.NewSemanticError("andExpr must have at least one equalityExpr")
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
		return nil, galaerr.NewSemanticError("equalityExpr must have at least one relationalExpr")
	}

	result, err := t.transformRelationalExpr(relExprs[0].(*grammar.RelationalExprContext))
	if err != nil {
		return nil, err
	}

	// Get the operators between expressions
	for i := 1; i < len(relExprs); i++ {
		// The operator is at position (i*2 - 1) in children
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
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
		return nil, galaerr.NewSemanticError("relationalExpr must have at least one additiveExpr")
	}

	result, err := t.transformAdditiveExpr(addExprs[0].(*grammar.AdditiveExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(addExprs); i++ {
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
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
		return nil, galaerr.NewSemanticError("additiveExpr must have at least one multiplicativeExpr")
	}

	result, err := t.transformMultiplicativeExpr(mulExprs[0].(*grammar.MultiplicativeExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(mulExprs); i++ {
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
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
		return nil, galaerr.NewSemanticError("multiplicativeExpr must have at least one unaryExpr")
	}

	result, err := t.transformUnaryExpr(unaryExprs[0].(*grammar.UnaryExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(unaryExprs); i++ {
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
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

	return nil, galaerr.NewSemanticError("unaryExpr must have unaryOp or postfixExpr")
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
	primary := primaryExpr.(*grammar.PrimaryExprContext).Primary()
	if primary == nil {
		return ""
	}
	primaryCtx := primary.(*grammar.PrimaryContext)
	if primaryCtx.Identifier() == nil {
		return ""
	}
	name := primaryCtx.Identifier().GetText()
	if t.isVal(name) {
		return name
	}
	return ""
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
	// primaryExpr -> primary
	return primaryExpr.(*grammar.PrimaryExprContext).Primary().(*grammar.PrimaryContext)
}

// getCallPatternFromExpression checks if an expression is a call pattern like Left(n)
// and returns the base expression context and argument list.
// Returns nil values if not a call pattern.
func (t *galaASTTransformer) getCallPatternFromExpression(ctx grammar.IExpressionContext) (*grammar.PrimaryExprContext, *grammar.ArgumentListContext) {
	primaryExpr, argList, _ := t.getCallPatternWithTypeArgsFromExpression(ctx)
	return primaryExpr, argList
}

// getCallPatternWithTypeArgsFromExpression checks if an expression is a call pattern
// and returns the base expression context, argument list, and any explicit type arguments.
// This handles both simple patterns like Left(n) and generic patterns like Unwrap[int](v).
// Returns nil values if not a call pattern.
func (t *galaASTTransformer) getCallPatternWithTypeArgsFromExpression(ctx grammar.IExpressionContext) (*grammar.PrimaryExprContext, *grammar.ArgumentListContext, *grammar.ExpressionListContext) {
	if ctx == nil {
		return nil, nil, nil
	}
	// Navigate through: expression -> orExpr -> andExpr -> equalityExpr -> relationalExpr -> additiveExpr -> multiplicativeExpr -> unaryExpr -> postfixExpr
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return nil, nil, nil
	}
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) == 0 || len(andExprs) > 1 {
		return nil, nil, nil // Not a simple expression
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) == 0 || len(eqExprs) > 1 {
		return nil, nil, nil
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) == 0 || len(relExprs) > 1 {
		return nil, nil, nil
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) == 0 || len(addExprs) > 1 {
		return nil, nil, nil
	}
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) == 0 || len(mulExprs) > 1 {
		return nil, nil, nil
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) == 0 || len(unaryExprs) > 1 {
		return nil, nil, nil
	}
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)
	// Check if there's a unary operator (like !)
	if unaryCtx.UnaryOp() != nil {
		return nil, nil, nil
	}
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil, nil, nil
	}
	postfixCtx := postfixExpr.(*grammar.PostfixExprContext)

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
	default:
		return token.ILLEGAL
	}
}

// transformPrimary, transformCompositeLiteral, transformLiteral moved to constructors.go
// Lambda-related functions moved to lambdas.go
// findLambdaInExpression moved to lambdas.go
func (t *galaASTTransformer) transformIfExpression(ctx *grammar.IfExpressionContext) (ast.Expr, error) {
	// 'if' '(' cond ')' thenExpr 'else' elseExpr
	cond, err := t.transformExpression(ctx.Expression(0))
	if err != nil {
		return nil, err
	}
	thenExpr, err := t.transformExpression(ctx.Expression(1))
	if err != nil {
		return nil, err
	}
	elseExpr, err := t.transformExpression(ctx.Expression(2))
	if err != nil {
		return nil, err
	}

	retType := transpiler.Type(transpiler.NilType{})
	if inferred, err := t.inferIfType(cond, thenExpr, elseExpr); err == nil && !inferred.IsNil() {
		retType = inferred
	}

	retTypeExpr := t.typeToExpr(retType)

	// Transpile to IIFE: func() T { if cond { return thenExpr }; return elseExpr }()
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
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.ReturnStmt{Results: []ast.Expr{thenExpr}},
							},
						},
					},
					&ast.ReturnStmt{Results: []ast.Expr{elseExpr}},
				},
			},
		},
	}, nil
}

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
