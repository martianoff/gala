package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains lambda expression and partial function transformation logic extracted from expressions.go
// Functions: transformLambda, transformLambdaWithExpectedType, inferBlockReturnType, isNewImmutableCall,
//            findPartialFunction*, findLambdaInExpression, transformPartialFunctionLiteral,
//            inferPartialFunctionParamType, transformPartialCaseClause, wrapInSome, generateNoneReturn,
//            wrapBlockReturnsInSome

func (t *galaASTTransformer) transformLambda(ctx *grammar.LambdaExpressionContext) (ast.Expr, error) {
	return t.transformLambdaWithExpectedType(ctx, nil)
}

// ExpectedVoid is a sentinel value indicating the lambda should have no return type
var ExpectedVoid ast.Expr = &ast.Ident{Name: "__void__"}

func (t *galaASTTransformer) transformLambdaWithExpectedType(ctx *grammar.LambdaExpressionContext, expectedRetType ast.Expr) (ast.Expr, error) {
	t.pushScope()
	defer t.popScope()
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			field, err := t.transformParameter(pCtx.(*grammar.ParameterContext))
			if err != nil {
				return nil, err
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var body *ast.BlockStmt
	var retType ast.Expr = ast.NewIdent("any")
	isVoidExpected := expectedRetType == ExpectedVoid

	// Check if expected type is a concrete type (not "any" or containing "any")
	// We only use the expected type if it's more specific than "any"
	isConcreteExpectedType := expectedRetType != nil && expectedRetType != ExpectedVoid && !containsAny(expectedRetType)

	// Use expected return type if provided and concrete
	if isConcreteExpectedType {
		retType = expectedRetType
	}

	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		// Try to infer return type from the block's return statements
		if !isConcreteExpectedType && !isVoidExpected {
			if inferredType := t.inferBlockReturnType(b); inferredType != nil {
				retType = inferredType
			} else {
				// Only add return nil if we couldn't infer a type AND block doesn't already end with return
				if !blockEndsWithReturn(b) {
					b.List = append(b.List, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("nil")}})
				}
			}
		}
		// Convert trailing expression statement to return statement for non-void lambdas.
		// This handles cases like: (x int) => { if (cond) Some(x) else None() }
		// where the if-else expression is the last statement but not wrapped in return.
		if !isVoidExpected && len(b.List) > 0 {
			if exprStmt, ok := b.List[len(b.List)-1].(*ast.ExprStmt); ok {
				b.List[len(b.List)-1] = &ast.ReturnStmt{Results: []ast.Expr{exprStmt.X}}
			}
		}
		body = b
	} else if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		// Use expected type if concrete, otherwise infer from expression
		if !isConcreteExpectedType && !isVoidExpected {
			retType = t.getExprType(expr)
		}
		if isVoidExpected {
			// For void functions, the expression is just a statement, not a return
			body = &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ExprStmt{X: expr},
				},
			}
		} else {
			body = &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{Results: []ast.Expr{expr}},
				},
			}
		}
	}

	// Build the function literal
	funcType := &ast.FuncType{
		Params: fieldList,
	}

	// Only add Results if not void
	if !isVoidExpected {
		funcType.Results = &ast.FieldList{
			List: []*ast.Field{
				{Type: retType},
			},
		}
	}

	return &ast.FuncLit{
		Type: funcType,
		Body: body,
	}, nil
}

// inferBlockReturnType tries to infer the return type from a block's return statements.
// Returns nil if no concrete type can be inferred.
func (t *galaASTTransformer) inferBlockReturnType(block *ast.BlockStmt) ast.Expr {
	// Build a map of val variable types from declarations in this block.
	// When we see `var result = std.NewImmutable(X)`, we record the type of X for `result`.
	valTypes := make(map[string]ast.Expr)
	for _, stmt := range block.List {
		// Handle val declarations: var result = std.NewImmutable(X)
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if genDecl, ok := declStmt.Decl.(*ast.GenDecl); ok && genDecl.Tok == token.VAR {
				for _, spec := range genDecl.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for i, name := range valueSpec.Names {
							if i < len(valueSpec.Values) {
								val := valueSpec.Values[i]
								// Check if this is a std.NewImmutable(X) call
								if callExpr, ok := val.(*ast.CallExpr); ok {
									if t.isNewImmutableCall(callExpr) && len(callExpr.Args) > 0 {
										// Store the type of the inner expression
										innerType := t.getExprType(callExpr.Args[0])
										if innerType != nil {
											valTypes[name.Name] = innerType
										}
									}
								}
							}
						}
					}
				}
			}
		}
		// Also handle short var declarations: result := std.NewImmutable(X)
		if assignStmt, ok := stmt.(*ast.AssignStmt); ok && assignStmt.Tok == token.DEFINE {
			for i, lhs := range assignStmt.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && i < len(assignStmt.Rhs) {
					rhs := assignStmt.Rhs[i]
					// Check if this is a std.NewImmutable(X) call
					if callExpr, ok := rhs.(*ast.CallExpr); ok {
						if t.isNewImmutableCall(callExpr) && len(callExpr.Args) > 0 {
							// Store the type of the inner expression
							innerType := t.getExprType(callExpr.Args[0])
							if innerType != nil {
								valTypes[ident.Name] = innerType
							}
						}
					}
				}
			}
		}
	}

	for _, stmt := range block.List {
		if retStmt, ok := stmt.(*ast.ReturnStmt); ok {
			if len(retStmt.Results) > 0 {
				result := retStmt.Results[0]
				// Try to infer type using valTypes for .Get() calls anywhere in the expression
				if typ := t.inferExprTypeWithValTypes(result, valTypes); typ != nil {
					return typ
				}
				// Fallback to direct type inference
				inferredType := t.getExprType(result)
				if inferredType != nil {
					if ident, ok := inferredType.(*ast.Ident); ok && ident.Name != "any" {
						return inferredType
					}
					// For non-ident types (like generic types), also return them
					if _, ok := inferredType.(*ast.Ident); !ok {
						return inferredType
					}
				}
			}
		}
	}
	return nil
}

// inferExprTypeWithValTypes tries to infer the type of an expression,
// using valTypes to resolve .Get() calls on local val variables.
// This handles expressions like `x.Get() + 2` where x is a val declared in the block.
// It recursively searches for .Get() calls and uses valTypes for lookup,
// delegating other type inference to the existing getExprType.
func (t *galaASTTransformer) inferExprTypeWithValTypes(expr ast.Expr, valTypes map[string]ast.Expr) ast.Expr {
	// Try to find a .Get() call on a val variable anywhere in the expression
	if typ := t.findValGetType(expr, valTypes); typ != nil {
		return typ
	}
	return nil
}

// findValGetType recursively searches for .Get() calls on val variables in the expression.
// Returns the type from valTypes if found, nil otherwise.
func (t *galaASTTransformer) findValGetType(expr ast.Expr, valTypes map[string]ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		// Check left operand first, then right
		if typ := t.findValGetType(e.X, valTypes); typ != nil {
			return typ
		}
		return t.findValGetType(e.Y, valTypes)
	case *ast.CallExpr:
		// Check for .Get() call on a val variable
		if selExpr, ok := e.Fun.(*ast.SelectorExpr); ok && selExpr.Sel.Name == "Get" {
			if ident, ok := selExpr.X.(*ast.Ident); ok {
				if typ, ok := valTypes[ident.Name]; ok {
					return typ
				}
			}
		}
		// Check arguments
		for _, arg := range e.Args {
			if typ := t.findValGetType(arg, valTypes); typ != nil {
				return typ
			}
		}
	case *ast.ParenExpr:
		return t.findValGetType(e.X, valTypes)
	case *ast.UnaryExpr:
		return t.findValGetType(e.X, valTypes)
	}
	return nil
}

// isNewImmutableCall checks if a call expression is a call to std.NewImmutable
func (t *galaASTTransformer) isNewImmutableCall(call *ast.CallExpr) bool {
	if selExpr, ok := call.Fun.(*ast.SelectorExpr); ok {
		if selExpr.Sel.Name == "NewImmutable" {
			if ident, ok := selExpr.X.(*ast.Ident); ok {
				return registry.Global.IsPreludePackage(ident.Name)
			}
		}
	}
	return false
}

// transformArgumentWithExpectedType transforms an argument expression, using the expected
// parameter type to properly type lambda expressions and partial function literals.
// transformArgumentWithExpectedType moved to calls.go

// findPartialFunctionInExpression traverses the expression tree to find a partial function literal
func (t *galaASTTransformer) findPartialFunctionInExpression(exprCtx grammar.IExpressionContext) *grammar.PartialFunctionLiteralContext {
	if exprCtx == nil {
		return nil
	}

	// Walk down the expression tree following the grammar structure
	// expression -> orExpr -> andExpr -> ... -> postfixExpr -> primaryExpr -> partialFunctionLiteral
	switch ctx := exprCtx.(type) {
	case *grammar.ExpressionContext:
		if ctx.OrExpr() != nil {
			return t.findPartialFunctionInOrExpr(ctx.OrExpr().(*grammar.OrExprContext))
		}
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInOrExpr(ctx *grammar.OrExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	andExprs := ctx.AllAndExpr()
	if len(andExprs) == 1 {
		return t.findPartialFunctionInAndExpr(andExprs[0].(*grammar.AndExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInAndExpr(ctx *grammar.AndExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	eqExprs := ctx.AllEqualityExpr()
	if len(eqExprs) == 1 {
		return t.findPartialFunctionInEqualityExpr(eqExprs[0].(*grammar.EqualityExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInEqualityExpr(ctx *grammar.EqualityExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	relExprs := ctx.AllRelationalExpr()
	if len(relExprs) == 1 {
		return t.findPartialFunctionInRelationalExpr(relExprs[0].(*grammar.RelationalExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInRelationalExpr(ctx *grammar.RelationalExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	addExprs := ctx.AllAdditiveExpr()
	if len(addExprs) == 1 {
		return t.findPartialFunctionInAdditiveExpr(addExprs[0].(*grammar.AdditiveExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInAdditiveExpr(ctx *grammar.AdditiveExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	mulExprs := ctx.AllMultiplicativeExpr()
	if len(mulExprs) == 1 {
		return t.findPartialFunctionInMultiplicativeExpr(mulExprs[0].(*grammar.MultiplicativeExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInMultiplicativeExpr(ctx *grammar.MultiplicativeExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	unaryExprs := ctx.AllUnaryExpr()
	if len(unaryExprs) == 1 {
		return t.findPartialFunctionInUnaryExpr(unaryExprs[0].(*grammar.UnaryExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInUnaryExpr(ctx *grammar.UnaryExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	if ctx.PostfixExpr() != nil {
		return t.findPartialFunctionInPostfixExpr(ctx.PostfixExpr().(*grammar.PostfixExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInPostfixExpr(ctx *grammar.PostfixExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	if ctx.PrimaryExpr() != nil {
		return t.findPartialFunctionInPrimaryExpr(ctx.PrimaryExpr().(*grammar.PrimaryExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInPrimaryExpr(ctx *grammar.PrimaryExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	if pf := ctx.PartialFunctionLiteral(); pf != nil {
		return pf.(*grammar.PartialFunctionLiteralContext)
	}
	return nil
}

// containsAny checks if the given type expression contains "any" as a type or type parameter.
// This is used to determine if an expected type is concrete enough to use for lambda return type.
func containsAny(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "any"
	case *ast.IndexExpr:
		// Generic type like Option[any]
		return containsAny(e.X) || containsAny(e.Index)
	case *ast.IndexListExpr:
		// Multiple type args like Map[K, V]
		if containsAny(e.X) {
			return true
		}
		for _, idx := range e.Indices {
			if containsAny(idx) {
				return true
			}
		}
		return false
	case *ast.SelectorExpr:
		// pkg.Type - check X for any
		return containsAny(e.X)
	case *ast.StarExpr:
		return containsAny(e.X)
	case *ast.ArrayType:
		return containsAny(e.Elt)
	case *ast.MapType:
		return containsAny(e.Key) || containsAny(e.Value)
	case *ast.FuncType:
		if e.Params != nil {
			for _, f := range e.Params.List {
				if containsAny(f.Type) {
					return true
				}
			}
		}
		if e.Results != nil {
			for _, f := range e.Results.List {
				if containsAny(f.Type) {
					return true
				}
			}
		}
		return false
	}
	return false
}

// findLambdaInExpression traverses the expression tree to find a lambda expression
// if the expression is simply a lambda (not part of a larger expression).

func (t *galaASTTransformer) findLambdaInExpression(exprCtx grammar.IExpressionContext) *grammar.LambdaExpressionContext {
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
	// Check that there are no postfix suffixes (no method calls, indexing, etc.)
	if len(postfixCtx.AllPostfixSuffix()) > 0 {
		return nil
	}
	primExpr := postfixCtx.PrimaryExpr()
	if primExpr == nil {
		return nil
	}
	primCtx := primExpr.(*grammar.PrimaryExprContext)
	lambdaExpr := primCtx.LambdaExpression()
	if lambdaExpr == nil {
		return nil
	}
	return lambdaExpr.(*grammar.LambdaExpressionContext)
}

func (t *galaASTTransformer) transformPartialFunctionLiteral(ctx *grammar.PartialFunctionLiteralContext, expectedType transpiler.Type) (ast.Expr, error) {
	caseClauses := ctx.AllCaseClause()
	if len(caseClauses) == 0 {
		return nil, galaerr.NewSemanticError("partial function must have at least one case")
	}

	// Try to infer parameter type from expected function type or from patterns
	var paramType transpiler.Type
	if expectedType != nil {
		if funcType, ok := expectedType.(transpiler.FuncType); ok && len(funcType.Params) > 0 {
			paramType = funcType.Params[0]
		}
	}

	// If we couldn't infer from context, try to infer from the patterns themselves
	if paramType == nil || paramType.IsNil() {
		paramType = t.inferPartialFunctionParamType(caseClauses)
	}

	// Fall back to 'any' if we still can't infer
	if paramType == nil || paramType.IsNil() {
		paramType = transpiler.BasicType{Name: "any"}
	}

	paramName := "_pf_arg"
	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, paramType)

	var clauses []ast.Stmt
	var resultTypes []transpiler.Type
	var casePatterns []string

	// Transform each case clause, wrapping results in Some(...)
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()

		// Even wildcard case gets wrapped in Some for partial functions
		clause, resultType, err := t.transformPartialCaseClause(ccCtx, paramName, paramType)
		if err != nil {
			return nil, err
		}
		if clause != nil {
			clauses = append(clauses, clause)
		}
		if resultType != nil {
			resultTypes = append(resultTypes, resultType)
			casePatterns = append(casePatterns, fmt.Sprintf("case %s", patternText))
		}
	}

	// Infer common inner result type T from all branches
	innerResultType, err := t.inferCommonResultType(resultTypes, casePatterns)
	if err != nil {
		return nil, err
	}

	if innerResultType == nil || innerResultType.IsNil() {
		innerResultType = transpiler.BasicType{Name: "any"}
	}

	if t.typeHasUnresolvedParams(innerResultType) {
		innerResultType = transpiler.BasicType{Name: "any"}
	}

	// The final result type is Option[T]
	optionResultType := transpiler.GenericType{
		Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption},
		Params: []transpiler.Type{innerResultType},
	}

	// Generate default case: return None[T]()
	noneReturn := t.generateNoneReturn(innerResultType)

	// Build the function body
	var stmts []ast.Stmt
	stmts = append(stmts, clauses...)
	stmts = append(stmts, noneReturn)

	// Build function literal returning Option[T]
	t.needsStdImport = true
	funcLit := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: t.typeToExpr(paramType)}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(optionResultType)}}},
		},
		Body: &ast.BlockStmt{List: stmts},
	}

	return funcLit, nil
}

// inferPartialFunctionParamType tries to infer the parameter type from the patterns
func (t *galaASTTransformer) inferPartialFunctionParamType(caseClauses []grammar.ICaseClauseContext) transpiler.Type {
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patCtx := ccCtx.Pattern()

		// Check for typed pattern like "x: int"
		if tp, ok := patCtx.(*grammar.TypedPatternContext); ok {
			typeExpr, err := t.transformType(tp.Type_())
			if err == nil {
				return t.exprToType(typeExpr)
			}
		}

		// Check for extractor patterns like Some(n), Left(x), etc.
		if ep, ok := patCtx.(*grammar.ExpressionPatternContext); ok {
			patternText := ep.GetText()
			// Common Option patterns
			if strings.HasPrefix(patternText, "Some(") || patternText == "None()" {
				return transpiler.GenericType{
					Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption},
					Params: []transpiler.Type{transpiler.BasicType{Name: "any"}},
				}
			}
			// Common Either patterns
			if strings.HasPrefix(patternText, "Left(") || strings.HasPrefix(patternText, "Right(") {
				return transpiler.GenericType{
					Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeEither},
					Params: []transpiler.Type{transpiler.BasicType{Name: "any"}, transpiler.BasicType{Name: "any"}},
				}
			}
		}
	}
	return nil
}

// transformPartialCaseClause transforms a case clause for partial function
// The result expression is wrapped in Some(...)
func (t *galaASTTransformer) transformPartialCaseClause(ctx *grammar.CaseClauseContext, paramName string, matchedType transpiler.Type) (ast.Stmt, transpiler.Type, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPatternWithType(patCtx, ast.NewIdent(paramName), matchedType)
	if err != nil {
		return nil, nil, err
	}

	// Handle guard clause
	if ctx.GetGuard() != nil {
		guard, err := t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guard,
		}
	}

	var body []ast.Stmt
	var resultType transpiler.Type

	if ctx.GetBodyBlock() != nil {
		b, err := t.transformBlock(ctx.GetBodyBlock().(*grammar.BlockContext))
		if err != nil {
			return nil, nil, err
		}
		// Wrap the last expression/return in Some
		body = t.wrapBlockReturnsInSome(b.List)
		// Infer type from the original (unwrapped) last statement
		if len(b.List) > 0 {
			if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
				resultType = t.inferResultType(ret.Results[0])
			}
		}
	} else if ctx.GetBody() != nil {
		expr, err := t.transformExpression(ctx.GetBody())
		if err != nil {
			return nil, nil, err
		}
		// Wrap in Some(expr)
		someCall := t.wrapInSome(expr)
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{someCall}}}
		resultType = t.inferResultType(expr)
	}

	bodyBlock := &ast.BlockStmt{List: body}
	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: bodyBlock,
	}

	if len(bindings) > 0 {
		return &ast.BlockStmt{
			List: append(bindings, ifStmt),
		}, resultType, nil
	}

	return ifStmt, resultType, nil
}

// wrapInSome generates: Some[T]{}.Apply(expr)
func (t *galaASTTransformer) wrapInSome(expr ast.Expr) ast.Expr {
	// Infer the type of expr for the type parameter
	exprType := t.getExprTypeNameManual(expr)
	if exprType == nil || exprType.IsNil() {
		exprType, _ = t.inferExprType(expr)
	}

	var someTypeExpr ast.Expr
	if exprType != nil && !exprType.IsNil() && exprType.String() != "any" {
		someTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("Some"),
			Index: t.typeToExpr(exprType),
		}
	} else {
		someTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("Some"),
			Index: ast.NewIdent("any"),
		}
	}

	// Generate: Some[T]{}.Apply(expr)
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: someTypeExpr},
			Sel: ast.NewIdent("Apply"),
		},
		Args: []ast.Expr{expr},
	}
}

// generateNoneReturn generates: return None[T]{}.Apply()
func (t *galaASTTransformer) generateNoneReturn(innerType transpiler.Type) ast.Stmt {
	var noneTypeExpr ast.Expr
	if innerType != nil && !innerType.IsNil() && innerType.String() != "any" {
		noneTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("None"),
			Index: t.typeToExpr(innerType),
		}
	} else {
		noneTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("None"),
			Index: ast.NewIdent("any"),
		}
	}

	// Generate: return None[T]{}.Apply()
	noneCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: noneTypeExpr},
			Sel: ast.NewIdent("Apply"),
		},
	}

	return &ast.ReturnStmt{Results: []ast.Expr{noneCall}}
}

// wrapBlockReturnsInSome wraps the final return/expression in a block with Some
func (t *galaASTTransformer) wrapBlockReturnsInSome(stmts []ast.Stmt) []ast.Stmt {
	if len(stmts) == 0 {
		return stmts
	}

	result := make([]ast.Stmt, len(stmts))
	copy(result, stmts)

	// Only wrap the last statement if it's a return
	last := result[len(result)-1]
	if ret, ok := last.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
		result[len(result)-1] = &ast.ReturnStmt{
			Results: []ast.Expr{t.wrapInSome(ret.Results[0])},
		}
	}

	return result
}

// blockEndsWithReturn checks if a block's last statement is a return statement.
// This is used to avoid adding duplicate return statements.
func blockEndsWithReturn(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}
	_, ok := block.List[len(block.List)-1].(*ast.ReturnStmt)
	return ok
}

// isGenericMethodName checks if a method is marked as generic for a given type name
// isGenericMethodName, isGenericMethodWithImports, isMethodGenericViaTypeMeta moved to calls.go
