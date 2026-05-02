package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

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
	return t.transformLambdaWithExpectedType(ctx, nil, nil)
}

// ExpectedVoid is a sentinel value indicating the lambda should have no return type
var ExpectedVoid ast.Expr = &ast.Ident{Name: "__void__"}

func (t *galaASTTransformer) transformLambdaWithExpectedType(ctx *grammar.LambdaExpressionContext, expectedRetType ast.Expr, expectedParamTypes []transpiler.Type) (ast.Expr, error) {
	t.pushScope()
	defer t.popScope()
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		allParams := paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter()
		// Validate: reject `_` as a type annotation when other params have explicit types.
		// `_` means "infer" but has no inference context in a standalone lambda.
		hasExplicitType := false
		hasWildcardType := false
		for _, pCtx := range allParams {
			paramCtx := pCtx.(*grammar.ParameterContext)
			if paramCtx.Type_() != nil {
				typeText := paramCtx.Type_().GetText()
				if typeText == "_" {
					hasWildcardType = true
				} else {
					hasExplicitType = true
				}
			}
		}
		if hasExplicitType && hasWildcardType {
			return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "cannot use '_' as a parameter type when other parameters have explicit types — provide the type explicitly or omit all parameter types for inference")
		}
		for i, pCtx := range allParams {
			paramCtx := pCtx.(*grammar.ParameterContext)
			field, err := t.transformParameter(paramCtx)
			if err != nil {
				return nil, err
			}
			// If param has no type annotation and we have an expected type, use it
			if paramCtx.Type_() == nil && expectedParamTypes != nil && i < len(expectedParamTypes) {
				expType := expectedParamTypes[i]
				if expType != nil && !expType.IsNil() && !expType.IsAny() {
					typeExpr := t.typeToExpr(expType)
					name := paramCtx.Identifier().GetText()
					isVal := paramCtx.VAL() != nil
					if isVal {
						field.Type = &ast.IndexExpr{X: t.stdIdent("Immutable"), Index: typeExpr}
						t.addVal(name, expType)
					} else {
						field.Type = typeExpr
						t.addVar(name, expType)
					}
					pos := transpiler.PosFromToken(paramCtx.Identifier().GetStart())
					t.lspLambdaParamHints = append(t.lspLambdaParamHints, transpiler.LambdaParamHint{
						Line:   pos.Line,
						Column: pos.Column,
						Name:   name,
						Type:   expType,
					})
				}
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var body *ast.BlockStmt
	var retType ast.Expr // nil = void
	isVoidExpected := expectedRetType == ExpectedVoid

	// Check if expected type is a concrete type (not "any" or containing "any")
	// We only use the expected type if it's more specific than "any"
	isConcreteExpectedType := expectedRetType != nil && expectedRetType != ExpectedVoid && !containsAny(expectedRetType)

	// True when the caller has supplied a function-shaped expectation that
	// requires a return value (non-void), even if the precise return type is
	// still unresolved (`any`). Used by transformBlockLambdaBody to decide
	// whether a trailing block expression should be promoted to a return.
	// Distinct from isConcreteExpectedType (which is false when the result is
	// `any`) and from isVoidExpected (which means no return is expected).
	expectsReturnValue := expectedRetType != nil && !isVoidExpected

	// Explicit return type declared on the lambda (e.g. `(x int) int => ...`).
	// When present it wins over both the expected type supplied by the caller
	// and any body-based inference: the user asked for a specific shape.
	var explicitRetType ast.Expr
	if ctx.Type_() != nil {
		rt, err := t.transformType(ctx.Type_().(*grammar.TypeContext))
		if err != nil {
			return nil, err
		}
		explicitRetType = rt
	}

	// Use expected return type if provided and concrete
	if isConcreteExpectedType {
		retType = expectedRetType
	}
	// An explicit annotation trumps caller-supplied expectations.
	if explicitRetType != nil {
		retType = explicitRetType
		isConcreteExpectedType = true
		expectsReturnValue = true
	}

	// Track the current function's return type for nested match expression fallback.
	// When a match expression inside a lambda can't infer branch types (e.g., branches
	// call methods from pure Go packages), it falls back to the enclosing return type.
	prevFuncReturnType := t.currentFuncReturnType
	if isConcreteExpectedType {
		t.currentFuncReturnType = t.astTypeToTranspilerType(expectedRetType)
	}
	defer func() { t.currentFuncReturnType = prevFuncReturnType }()

	if ctx.Block() != nil {
		b, inferredRet, err := t.transformBlockLambdaBody(ctx, isVoidExpected, isConcreteExpectedType, expectsReturnValue)
		if err != nil {
			return nil, err
		}
		body = b
		if inferredRet != nil {
			retType = inferredRet
		}
	} else if ctx.Expression() != nil {
		b, inferredRet, err := t.transformExpressionLambdaBody(ctx, isVoidExpected, isConcreteExpectedType)
		if err != nil {
			return nil, err
		}
		body = b
		if inferredRet != nil {
			retType = inferredRet
		}
	}

	// Build the function literal
	funcType := &ast.FuncType{
		Params: fieldList,
	}

	// Only add Results if not void (retType != nil)
	if retType != nil {
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

// transformBlockLambdaBody handles the `{ ... }` form of a lambda body.
// It returns (body, inferredReturnType, error). inferredReturnType is nil
// when the expected return type is already concrete (caller keeps its own)
// or when the lambda is void. Extracted from transformLambdaWithExpectedType
// as part of A6.
func (t *galaASTTransformer) transformBlockLambdaBody(ctx *grammar.LambdaExpressionContext, isVoidExpected, isConcreteExpectedType, expectsReturnValue bool) (*ast.BlockStmt, ast.Expr, error) {
	// Signal to transformBlock that the lambda body's last expression is
	// promoted to the implicit return when this lambda is value-returning.
	// Without this, a trailing bare `match` whose value becomes the lambda's
	// return would be marked statement-position and forced to void.
	if !isVoidExpected {
		t.blockLastStmtIsValue = true
	}
	b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
	if err != nil {
		return nil, nil, err
	}
	// Reject block lambdas in void context that discard error returns.
	if isVoidExpected {
		for _, stmt := range b.List {
			if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
				if funcName := t.goCallReturnsErrorOnly(exprStmt.X); funcName != "" {
					return nil, nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						fmt.Sprintf("cannot discard error return from %s in void lambda — use FromError(%s) to handle the error", funcName, funcName))
				}
			}
		}
	}
	// When void is expected, strip any trailing "return nil" (legacy pattern).
	if isVoidExpected && len(b.List) > 0 {
		if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			if ident, ok := ret.Results[0].(*ast.Ident); ok && ident.Name == "nil" {
				b.List = b.List[:len(b.List)-1]
			}
		}
	}
	// Convert trailing expression statement to a return statement for non-void
	// lambdas. GALA's grammar treats the last expression of a block as the
	// implicit return value (no `return` keyword needed). Two cases are promoted:
	//   1. IIFEs (CallExpr wrapping FuncLit) — match/if-expressions compile to
	//      these and we can always trust them to produce a value. This is safe
	//      regardless of caller context (even with no expected type, since the
	//      promotion gives downstream type inference something to work with).
	//   2. Arbitrary trailing expressions, but ONLY when the caller has
	//      signalled that the lambda must produce a value (expectsReturnValue).
	//      Without that signal, we cannot tell whether a block like
	//      `{ doSideEffect() }` is meant to discard the call's result (void
	//      lambda) or to forward it (value lambda), so we keep the existing
	//      void interpretation. Inside a generic method like `Array.Map[U]`,
	//      the caller-supplied FuncType sets expectsReturnValue=true even when
	//      U is still unresolved (`any`), which lets us infer U from the
	//      trailing expression's type — including val-bound identifiers whose
	//      static type may not be reachable from getExprTypeName (e.g. they
	//      compile to `result.Get()` on an Immutable wrapper).
	//
	// A trailing void IIFE is NOT promoted: it has no value to return. This
	// fires when the lambda body terminates in a statement-position match
	// whose every arm is void (assignment / `{}` / void call). Promoting it
	// would emit `return <void-IIFE>`, which Go rejects with "(no value)
	// used as value". Leaving it as a bare expression statement lets the
	// lambda's return type fall through to void.
	if !isVoidExpected && len(b.List) > 0 {
		if exprStmt, ok := b.List[len(b.List)-1].(*ast.ExprStmt); ok {
			shouldPromote := (isIIFE(exprStmt.X) || expectsReturnValue) && !isVoidIIFE(exprStmt.X)
			if shouldPromote {
				b.List[len(b.List)-1] = &ast.ReturnStmt{Results: []ast.Expr{exprStmt.X}}
			}
		}
	}
	var retType ast.Expr
	if !isConcreteExpectedType && !isVoidExpected {
		if inferredType := t.inferBlockReturnType(b); inferredType != nil {
			// `inferBlockReturnType` may surface VoidType (rendered as the
			// literal ident `void`) when every observed return path's
			// inferred type was VoidType — e.g. all `return` statements
			// reference void IIFEs that the previous block left untouched.
			// `void` is not a valid Go return type; treat it the same way
			// we treat "no return type detected" and let the lambda be
			// emitted without a Results clause.
			if !isVoidTypeIdent(inferredType) {
				retType = inferredType
			}
		}
		// If retType is still nil (void), strip trailing "return nil" from the block.
		if retType == nil && len(b.List) > 0 {
			if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
				if ident, ok := ret.Results[0].(*ast.Ident); ok && ident.Name == "nil" {
					b.List = b.List[:len(b.List)-1]
				}
			}
		}
	}
	return b, retType, nil
}

// isVoidIIFE reports whether expr is a CallExpr whose callee is a FuncLit
// declaring no return type (i.e. the immediately-invoked function returns
// nothing). Match expressions whose every arm is void lower to a void IIFE
// in this exact shape, and they must NOT be promoted to `return <expr>`.
func isVoidIIFE(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	funcLit, ok := call.Fun.(*ast.FuncLit)
	if !ok {
		return false
	}
	if funcLit.Type == nil {
		return false
	}
	if funcLit.Type.Results == nil {
		return true
	}
	return len(funcLit.Type.Results.List) == 0
}

// isVoidTypeIdent reports whether expr is the literal ident `void`. The
// transpiler synthesises this name when typeToExpr is asked for a VoidType
// fallthrough — useful as a sentinel internally, but invalid Go in an
// emitted FuncType.Results clause.
func isVoidTypeIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "void"
}

// transformExpressionLambdaBody handles the `=> expr` form of a lambda body.
// Returns (body, inferredReturnType, error). Extracted from
// transformLambdaWithExpectedType as part of A6.
func (t *galaASTTransformer) transformExpressionLambdaBody(ctx *grammar.LambdaExpressionContext, isVoidExpected, isConcreteExpectedType bool) (*ast.BlockStmt, ast.Expr, error) {
	expr, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, nil, err
	}
	var body *ast.BlockStmt
	var retType ast.Expr

	// Use expected type if concrete, otherwise infer from expression.
	if !isConcreteExpectedType && !isVoidExpected {
		// Expression lambda `() => nil` is treated as void.
		if ident, ok := expr.(*ast.Ident); ok && ident.Name == "nil" {
			body = &ast.BlockStmt{}
		}
		if body == nil {
			retType = t.getExprType(expr)
		}
	}
	if body != nil {
		// Already set (e.g., expression lambda `() => nil` treated as void).
		return body, retType, nil
	}
	if isVoidExpected {
		// Reject expression lambdas in void context that discard error returns.
		if funcName := t.goCallReturnsErrorOnly(expr); funcName != "" {
			return nil, nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
				fmt.Sprintf("cannot discard error return from %s in void lambda — use FromError(%s) to handle the error", funcName, funcName))
		}
		body = &ast.BlockStmt{
			List: []ast.Stmt{&ast.ExprStmt{X: expr}},
		}
		return body, retType, nil
	}
	if multiRetBody, multiRetType := t.tryWrapGoMultiReturnWithErrorPanic(expr); multiRetBody != nil {
		// Go function returning (T, error) or (A, B, error) in expression lambda.
		body = multiRetBody
		if multiRetType != nil && !isConcreteExpectedType {
			retType = multiRetType
		}
		return body, retType, nil
	}
	body = &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ReturnStmt{Results: []ast.Expr{expr}},
		},
	}
	return body, retType, nil
}

// goCallReturnsErrorOnly checks if expr is a call to a Go function whose sole return
// type is `error`. Returns the function name for the error message, or "" otherwise.
// This only catches error-only returns (e.g., Close(), ListenAndServe()), NOT multi-return
// functions like fmt.Println() which return (int, error) — those are handled by
// tryWrapGoMultiReturnWithErrorPanic in non-void contexts.
func (t *galaASTTransformer) goCallReturnsErrorOnly(expr ast.Expr) string {
	if t.goTypeInfo == nil {
		return ""
	}
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}

	var sig *transpiler.GoFuncSignature
	var funcName string
	switch fun := callExpr.Fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			funcName = id.Name + "." + fun.Sel.Name
			sig = t.goTypeInfo.GetFuncSignature(funcName)
			if sig == nil {
				sig = t.resolveMethodSignatureOnExpr(fun.X, fun.Sel.Name)
			}
		} else {
			sig = t.resolveMethodSignatureOnExpr(fun.X, fun.Sel.Name)
			if sel, ok := fun.X.(*ast.SelectorExpr); ok {
				funcName = sel.Sel.Name + "." + fun.Sel.Name
			} else {
				funcName = fun.Sel.Name
			}
		}
	}
	if sig == nil {
		return ""
	}

	// Only reject when the sole return type is error
	if len(sig.Returns) == 1 && sig.Returns[0] != nil && sig.Returns[0].String() == "error" {
		return funcName
	}
	return ""
}

// tryWrapGoMultiReturnWithErrorPanic checks if expr is a call to a Go function
// whose last return value is error. If so, generates a block that captures all
// returns, panics on error, and returns the non-error values.
//
// Supported patterns:
//   - (T, error)       -> returns T
//   - (A, B, error)    -> returns Tuple[A, B]
//   - (A, B, C, error) -> returns Tuple3[A, B, C]
//   - etc. up to Tuple10
//
// This enables patterns like Try(() => os.MkdirTemp("", "prefix-*")) where
// os.MkdirTemp returns (string, error) -- the error is converted to a panic
// which Try catches as Failure.
func (t *galaASTTransformer) tryWrapGoMultiReturnWithErrorPanic(expr ast.Expr) (*ast.BlockStmt, ast.Expr) {
	if t.goTypeInfo == nil {
		return nil, nil
	}
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, nil
	}

	// Extract the function signature from the call.
	// Case 1: simple pkg.Func() call — look up by qualified name.
	// Case 2: chained call like expr.Method() — resolve receiver type, then look up method.
	var sig *transpiler.GoFuncSignature
	switch fun := callExpr.Fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			// Simple case: pkg.Func() or receiver.Method() where receiver is an ident
			qualifiedName := id.Name + "." + fun.Sel.Name
			sig = t.goTypeInfo.GetFuncSignature(qualifiedName)
			if sig == nil {
				// Could be a method call on a variable — resolve its type
				sig = t.resolveMethodSignatureOnExpr(fun.X, fun.Sel.Name)
			}
		} else {
			// Chained call: e.g., exec.Command(...).Output()
			// Resolve the type of fun.X (the receiver expression) and look up the method
			sig = t.resolveMethodSignatureOnExpr(fun.X, fun.Sel.Name)
		}
	}
	if sig == nil || len(sig.Returns) < 2 {
		return nil, nil
	}

	// Check that the LAST return is error
	lastRet := sig.Returns[len(sig.Returns)-1]
	if lastRet == nil || lastRet.String() != "error" {
		return nil, nil
	}

	// Number of non-error return values
	valueCount := len(sig.Returns) - 1

	// Build LHS variable names: _v0, _v1, ... _vN, _err
	var lhsExprs []ast.Expr
	for i := 0; i < valueCount; i++ {
		lhsExprs = append(lhsExprs, ast.NewIdent(fmt.Sprintf("_v%d", i)))
	}
	lhsExprs = append(lhsExprs, ast.NewIdent("_err"))

	// Build the return expression and type
	var returnExpr ast.Expr
	var returnTypeExpr ast.Expr
	if valueCount == 1 {
		// (T, error) -> return _v0, type is T
		returnExpr = ast.NewIdent("_v0")
		returnTypeExpr = t.typeToExpr(sig.Returns[0])
	} else {
		// (A, B, error) -> return std.Tuple[A, B]{V1: _v0, V2: _v1}
		// (A, B, C, error) -> return std.Tuple3[A, B, C]{V1: _v0, V2: _v1, V3: _v2}
		// etc.
		tupleName, _ := tupleArityName(valueCount)

		// Build type args from the non-error return types
		var typeArgs []ast.Expr
		for i := 0; i < valueCount; i++ {
			typeArgs = append(typeArgs, t.typeToExpr(sig.Returns[i]))
		}

		// Build tuple type expression: std.Tuple[A, B] or std.Tuple3[A, B, C]
		var tupleType ast.Expr
		tupleBase := t.stdIdent(tupleName)
		if len(typeArgs) == 1 {
			tupleType = &ast.IndexExpr{X: tupleBase, Index: typeArgs[0]}
		} else {
			tupleType = &ast.IndexListExpr{X: tupleBase, Indices: typeArgs}
		}

		// Build field key-value pairs: V1: NewImmutable(_v0), V2: NewImmutable(_v1), ...
		// Tuple fields are Immutable[T] (val fields)
		var elts []ast.Expr
		for i := 0; i < valueCount; i++ {
			elts = append(elts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fmt.Sprintf("V%d", i+1)),
				Value: &ast.CallExpr{
					Fun:  t.stdIdent("NewImmutable"),
					Args: []ast.Expr{ast.NewIdent(fmt.Sprintf("_v%d", i))},
				},
			})
		}

		returnExpr = &ast.CompositeLit{
			Type: tupleType,
			Elts: elts,
		}
		returnTypeExpr = tupleType
		t.needsStdImport = true
	}

	return &ast.BlockStmt{
		List: []ast.Stmt{
			// _v0, _v1, ..., _err := goFunc(args...)
			&ast.AssignStmt{
				Lhs: lhsExprs,
				Tok: token.DEFINE,
				Rhs: []ast.Expr{callExpr},
			},
			// if _err != nil { panic(_err) }
			&ast.IfStmt{
				Cond: &ast.BinaryExpr{
					X:  ast.NewIdent("_err"),
					Op: token.NEQ,
					Y:  ast.NewIdent("nil"),
				},
				Body: &ast.BlockStmt{
					List: []ast.Stmt{
						&ast.ExprStmt{X: &ast.CallExpr{
							Fun:  ast.NewIdent("panic"),
							Args: []ast.Expr{ast.NewIdent("_err")},
						}},
					},
				},
			},
			// return _v0  (or std.Tuple[A,B]{V1: _v0, V2: _v1})
			&ast.ReturnStmt{Results: []ast.Expr{returnExpr}},
		},
	}, returnTypeExpr
}

// resolveMethodSignatureOnExpr resolves the type of an arbitrary expression, then looks up
// the method signature on that type. This handles chained calls like exec.Command(...).Output()
// where the receiver is a CallExpr rather than a simple Ident.
func (t *galaASTTransformer) resolveMethodSignatureOnExpr(receiver ast.Expr, methodName string) *transpiler.GoFuncSignature {
	if t.goTypeInfo == nil {
		return nil
	}
	receiverType := t.getExprTypeNameManual(receiver)
	if transpiler.IsUnusable(receiverType) {
		return nil
	}
	typeName := receiverType.String()
	// Strip pointer prefix for method lookup
	cleanType := strings.TrimPrefix(typeName, "*")

	// Try direct lookup
	if sig := t.goTypeInfo.GetMethodSignature(cleanType, methodName); sig != nil {
		return sig
	}

	// If the type is a Go type alias, resolve and try the underlying type's methods
	if aliasedType := t.goTypeInfo.ResolveTypeAlias(cleanType); aliasedType != nil {
		if sig := t.goTypeInfo.GetMethodSignature(aliasedType.String(), methodName); sig != nil {
			return sig
		}
	}

	return nil
}

// wrapGoMultiReturnAsIIFE wraps a Go function call returning (T, error) in an IIFE
// that destructures the return, panics on error, and returns the non-error value.
// If the expression is not a multi-return Go call, returns the original expression unchanged.
//
// Example: os.Create(path) which returns (*os.File, error) becomes:
//
//	func() *os.File { _v0, _err := os.Create(path); if _err != nil { panic(_err) }; return _v0 }()
func (t *galaASTTransformer) wrapGoMultiReturnAsIIFE(expr ast.Expr) ast.Expr {
	block, returnTypeExpr := t.tryWrapGoMultiReturnWithErrorPanic(expr)
	if block == nil {
		return expr
	}
	// Wrap in IIFE: func() T { ... }()
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: returnTypeExpr}},
				},
			},
			Body: block,
		},
	}
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

	// Collect return types from ALL return paths (including nested if/else blocks).
	var returnTypes []ast.Expr
	t.collectReturnTypes(block, valTypes, &returnTypes)

	if len(returnTypes) == 0 {
		return nil
	}
	if len(returnTypes) == 1 {
		return returnTypes[0]
	}

	// Unify all collected return types.
	return t.unifyBlockReturnTypes(returnTypes)
}

// collectReturnTypes recursively walks a block statement collecting inferred return
// types from every return statement, including those nested inside if/else blocks.
func (t *galaASTTransformer) collectReturnTypes(block *ast.BlockStmt, valTypes map[string]ast.Expr, out *[]ast.Expr) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if len(s.Results) > 0 {
				if typ := t.inferReturnExprType(s.Results[0], valTypes); typ != nil {
					*out = append(*out, typ)
				}
			}
		case *ast.IfStmt:
			// Recurse into if body and else branch
			t.collectReturnTypes(s.Body, valTypes, out)
			if s.Else != nil {
				switch els := s.Else.(type) {
				case *ast.BlockStmt:
					t.collectReturnTypes(els, valTypes, out)
				case *ast.IfStmt:
					// else-if chain: wrap in a synthetic block for recursion
					t.collectReturnTypes(&ast.BlockStmt{List: []ast.Stmt{els}}, valTypes, out)
				}
			}
		case *ast.BlockStmt:
			t.collectReturnTypes(s, valTypes, out)
		}
	}
}

// inferReturnExprType infers the type of a single return expression, trying
// valTypes-based inference first, then falling back to getExprType.
func (t *galaASTTransformer) inferReturnExprType(expr ast.Expr, valTypes map[string]ast.Expr) ast.Expr {
	// First try direct type inference (getExprTypeName) — this is the most reliable
	// as it uses the full type inference pipeline.
	inferredType := t.getExprType(expr)
	if inferredType != nil {
		if ident, ok := inferredType.(*ast.Ident); !ok || ident.Name != "any" {
			return inferredType
		}
	}
	// Fallback: try to infer type using valTypes for .Get() calls on val variables.
	// Only use this for top-level .Get() expressions (e.g., return result.Get()),
	// NOT for nested .Get() calls inside other expressions — otherwise we'd return
	// the inner val type instead of the outer expression type.
	if callExpr, ok := expr.(*ast.CallExpr); ok && len(callExpr.Args) == 0 {
		if selExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok && selExpr.Sel.Name == "Get" {
			if ident, ok := selExpr.X.(*ast.Ident); ok {
				if typ, ok := valTypes[ident.Name]; ok {
					return typ
				}
			}
		}
	}
	return nil
}

// unifyBlockReturnTypes takes a list of inferred return type expressions and attempts
// to find a single unified type. If all types are identical, that type is returned.
// If they differ only in generic type parameters, the most specific (non-primitive
// inner parameter) type is preferred. Returns nil if unification is not possible.
func (t *galaASTTransformer) unifyBlockReturnTypes(types []ast.Expr) ast.Expr {
	if len(types) == 0 {
		return nil
	}

	// Convert to transpiler types for structural comparison.
	tTypes := make([]transpiler.Type, len(types))
	for i, te := range types {
		tTypes[i] = t.astTypeToTranspilerType(te)
	}

	// Start with the first type as candidate.
	best := tTypes[0]
	bestExpr := types[0]

	for i := 1; i < len(tTypes); i++ {
		cur := tTypes[i]
		if best.String() == cur.String() {
			continue // identical
		}

		// Try to pick the more specific type.
		unified := t.pickMoreSpecificType(best, cur)
		if unified != nil {
			best = unified
			bestExpr = t.typeToExpr(unified)
			continue
		}

		// Types are incompatible — return the first non-primitive/non-simple type
		// as a best-effort (matches the old behaviour of picking the first return).
		// A future enhancement could emit a SemanticError here when we have access
		// to the ANTLR context for position info.
		return bestExpr
	}

	return bestExpr
}

// pickMoreSpecificType compares two types and returns the more specific one.
// For generic types with the same base (e.g., Future[string] vs Future[Response]),
// it picks the one whose inner type parameter is a non-primitive named type.
// Returns nil if neither is clearly more specific or the types are incompatible.
func (t *galaASTTransformer) pickMoreSpecificType(a, b transpiler.Type) transpiler.Type {
	genA, aIsGen := a.(transpiler.GenericType)
	genB, bIsGen := b.(transpiler.GenericType)

	if aIsGen && bIsGen {
		baseA := stripPackagePrefix(genA.Base.BaseName())
		baseB := stripPackagePrefix(genB.Base.BaseName())
		if baseA == baseB && len(genA.Params) == len(genB.Params) {
			// Same generic base — compare each type parameter.
			// Prefer the one with more specific (non-primitive) parameters.
			aScore := 0
			bScore := 0
			for i := range genA.Params {
				pa := genA.Params[i]
				pb := genB.Params[i]
				if pa.String() == pb.String() {
					continue
				}
				if isSimpleOrPrimitive(pa) && !isSimpleOrPrimitive(pb) {
					bScore++
				} else if !isSimpleOrPrimitive(pa) && isSimpleOrPrimitive(pb) {
					aScore++
				}
			}
			if aScore >= bScore {
				return a
			}
			return b
		}
	}

	// If one is generic and the other is a basic/named type with the same base name,
	// prefer the generic (more informative).
	if aIsGen && !bIsGen && stripPackagePrefix(genA.Base.BaseName()) == stripPackagePrefix(b.BaseName()) {
		return a
	}
	if bIsGen && !aIsGen && stripPackagePrefix(genB.Base.BaseName()) == stripPackagePrefix(a.BaseName()) {
		return b
	}

	return nil // incompatible
}

// isSimpleOrPrimitive returns true for primitive types (int, string, bool, float64, etc.)
// or types named "any".
func isSimpleOrPrimitive(typ transpiler.Type) bool {
	name := typ.BaseName()
	return transpiler.IsPrimitiveType(name) || name == "any"
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
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "partial function must have at least one case")
	}

	// Try to infer parameter type from expected function type or from patterns
	var paramType transpiler.Type
	if expectedType != nil {
		if funcType, ok := expectedType.(transpiler.FuncType); ok && len(funcType.Params) > 0 {
			paramType = funcType.Params[0]
		}
	}

	// If we couldn't infer from context, try to infer from the patterns themselves
	if transpiler.IsUnusable(paramType) {
		paramType = t.inferPartialFunctionParamType(caseClauses)
	}

	// Fall back to 'any' if we still can't infer
	if transpiler.IsUnusable(paramType) {
		t.warnInference("partial function param type defaulting to 'any' — could not infer from context")
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
	innerResultType, err := t.inferCommonResultType(resultTypes, casePatterns, ctx)
	if err != nil {
		return nil, err
	}

	if transpiler.IsUnusable(innerResultType) {
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
				return t.astTypeToTranspilerType(typeExpr)
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
		// Partial function arm body: the trailing expression is wrapped in
		// Some(...) below, so it is value-consumed.
		t.blockLastStmtIsValue = true
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
	} else if ctx.GetBodyStmt() != nil {
		if exprCtx := ctx.GetBodyStmt().Expression(); exprCtx != nil {
			expr, err := t.transformExpression(exprCtx)
			if err != nil {
				return nil, nil, err
			}
			// Wrap in Some(expr)
			someCall := t.wrapInSome(expr)
			body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{someCall}}}
			resultType = t.inferResultType(expr)
		} else {
			stmt, err := t.transformSimpleStatement(ctx.GetBodyStmt())
			if err != nil {
				return nil, nil, err
			}
			body = []ast.Stmt{stmt}
		}
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
	if transpiler.IsUnusable(exprType) {
		exprType, _ = t.inferExprType(expr)
	}

	var someTypeExpr ast.Expr
	if exprType != nil && !exprType.IsNil() && !exprType.IsAny() {
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
	if innerType != nil && !innerType.IsNil() && !innerType.IsAny() {
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

// isIIFE checks if an expression is an immediately-invoked function expression
// (a CallExpr whose Fun is a FuncLit). Match expressions and if-else expressions
// are compiled to IIFEs by the transpiler.
func isIIFE(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	// Check if Fun is a FuncLit (plain IIFE) or an IndexExpr/IndexListExpr wrapping a FuncLit
	// (generic IIFE, though rare)
	switch fun := call.Fun.(type) {
	case *ast.FuncLit:
		return true
	case *ast.IndexExpr:
		_, ok := fun.X.(*ast.FuncLit)
		return ok
	}
	return false
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

// ---- L4: Placeholder lambda shorthand (`_ * 2` ⇒ `(x) => x * 2`) ----
//
// When an expression contains `_` identifiers and is passed as an argument to
// a function expecting a function type, the transformer rewrites the
// expression into a lambda, replacing each `_` with a fresh parameter.
// Multiple underscores in a single expression become multiple parameters in
// left-to-right order, matching Scala semantics:
//
//	list.Map(_ * 2)               → list.Map((x) => x * 2)
//	list.Filter(_ > 0)            → list.Filter((x) => x > 0)
//	list.Map(_.Name)              → list.Map((x) => x.Name)
//	list.FoldLeft(0, _ + _)       → list.FoldLeft(0, (a, b) => a + b)
//
// The rewrite is gated on the argument's expected type being a function
// type. Outside that context, `_` is still only legal in pattern positions
// and yields a scope-lookup error at Go compile time.

// countPlaceholderUnderscoresInExpr walks the parse tree under exprCtx and
// counts leaf IDENTIFIER tokens whose text is `_`. This is a fast lexical
// check used to decide whether to attempt a placeholder-lambda rewrite —
// the actual renaming happens on the Go AST after transformation.
func countPlaceholderUnderscoresInExpr(exprCtx grammar.IExpressionContext) int {
	if exprCtx == nil {
		return 0
	}
	return countPlaceholderUnderscoresInTree(exprCtx)
}

// countPlaceholderUnderscoresInTree recursively counts `_` IDENTIFIER leaves
// in any ANTLR parse subtree. Lambda and pattern subtrees are skipped so
// that existing `_` uses in nested lambdas or pattern positions do not
// trigger the rewrite.
func countPlaceholderUnderscoresInTree(node antlr.Tree) int {
	if node == nil {
		return 0
	}
	// Stop at nested lambdas — their `_` uses belong to their own body.
	if _, ok := node.(*grammar.LambdaExpressionContext); ok {
		return 0
	}
	// Stop at pattern positions where `_` means wildcard, not placeholder.
	if _, ok := node.(*grammar.CaseClauseContext); ok {
		return 0
	}
	if tn, ok := node.(antlr.TerminalNode); ok {
		if tn.GetText() == "_" {
			return 1
		}
		return 0
	}
	total := 0
	for i := 0; i < node.GetChildCount(); i++ {
		total += countPlaceholderUnderscoresInTree(node.GetChild(i))
	}
	return total
}

// tryRewriteAsPlaceholderLambda is the L4 entry point called by
// transformArgumentWithExpectedType. It returns (expr, handled, error):
//
//	handled=false — the expression is not a placeholder lambda candidate;
//	                caller should fall through to ordinary expression
//	                transformation.
//	handled=true  — the expression was successfully rewritten into a lambda;
//	                the caller must use expr verbatim.
//
// Preconditions for a rewrite:
//  1. expectedType is a FuncType
//  2. The expression contains at least one `_` identifier in a
//     non-pattern, non-lambda position
//
// The rewrite installs fresh `_` bindings of type `any` (or the expected
// param type) in a new scope so ordinary expression transformation
// succeeds, then walks the produced Go AST and renames each `_` ident to a
// fresh `__pN`, finally wrapping the result in a `*ast.FuncLit` whose
// parameter list matches the expected FuncType.
func (t *galaASTTransformer) tryRewriteAsPlaceholderLambda(
	exprCtx grammar.IExpressionContext,
	expectedType transpiler.Type,
) (ast.Expr, bool, error) {
	ft, ok := expectedType.(transpiler.FuncType)
	if !ok {
		return nil, false, nil
	}
	placeholderCount := countPlaceholderUnderscoresInExpr(exprCtx)
	if placeholderCount == 0 {
		return nil, false, nil
	}

	// Choose the parameter types from the expected FuncType. If the expected
	// type has fewer params than the number of placeholders, pad with `any`
	// — we'd rather emit code that compiles than reject the call outright.
	paramTypes := make([]transpiler.Type, placeholderCount)
	for i := 0; i < placeholderCount; i++ {
		if i < len(ft.Params) && !ft.Params[i].IsNil() {
			paramTypes[i] = ft.Params[i]
		} else {
			paramTypes[i] = transpiler.BasicType{Name: "any"}
		}
	}

	// Install a fresh scope with `_` bound to `any` so the normal expression
	// transformation can resolve the identifier without producing a
	// scope-lookup error. The ident is renamed below after transformation.
	t.pushScope()
	t.addVar("_", transpiler.BasicType{Name: "any"})
	body, err := t.transformExpression(exprCtx)
	t.popScope()
	if err != nil {
		return nil, false, err
	}

	// Walk the produced AST and rename each `_` ident to a fresh parameter
	// name, left-to-right. We also record the param types at each position.
	paramNames := make([]string, 0, placeholderCount)
	var renameWalk func(parent ast.Node)
	renameWalk = func(n ast.Node) {
		if n == nil {
			return
		}
		// Manual walk so we can mutate *ast.Ident in place.
		switch node := n.(type) {
		case *ast.Ident:
			if node.Name == "_" {
				fresh := fmt.Sprintf("__p%d", len(paramNames))
				node.Name = fresh
				paramNames = append(paramNames, fresh)
			}
		case *ast.ParenExpr:
			renameWalk(node.X)
		case *ast.UnaryExpr:
			renameWalk(node.X)
		case *ast.BinaryExpr:
			renameWalk(node.X)
			renameWalk(node.Y)
		case *ast.SelectorExpr:
			renameWalk(node.X)
		case *ast.IndexExpr:
			renameWalk(node.X)
			renameWalk(node.Index)
		case *ast.CallExpr:
			renameWalk(node.Fun)
			for _, a := range node.Args {
				renameWalk(a)
			}
		case *ast.StarExpr:
			renameWalk(node.X)
		case *ast.CompositeLit:
			for _, e := range node.Elts {
				renameWalk(e)
			}
		case *ast.KeyValueExpr:
			renameWalk(node.Key)
			renameWalk(node.Value)
		}
	}
	renameWalk(body)

	if len(paramNames) == 0 {
		// Lexical scan found `_` tokens but none survived transformation
		// (they were in dead or comment positions). Fall through to ordinary
		// transformation.
		return nil, false, nil
	}

	// Build the lambda parameter list.
	fieldList := &ast.FieldList{}
	for i, name := range paramNames {
		typeExpr := t.typeToExpr(paramTypes[i])
		if typeExpr == nil {
			typeExpr = ast.NewIdent("any")
		}
		fieldList.List = append(fieldList.List, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(name)},
			Type:  typeExpr,
		})
	}

	// Build the result type from the expected FuncType if available.
	// When no result type is declared, infer from the body expression.
	var resultField *ast.FieldList
	if len(ft.Results) > 0 && !ft.Results[0].IsNil() {
		resultField = &ast.FieldList{
			List: []*ast.Field{{Type: t.typeToExpr(ft.Results[0])}},
		}
	} else {
		bodyType := t.getExprType(body)
		if bodyType != nil {
			resultField = &ast.FieldList{
				List: []*ast.Field{{Type: bodyType}},
			}
		}
	}

	funcLit := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  fieldList,
			Results: resultField,
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{body}}},
		},
	}
	return funcLit, true, nil
}

// isGenericMethodName checks if a method is marked as generic for a given type name
// isGenericMethodName, isGenericMethodWithImports, isMethodGenericViaTypeMeta moved to calls.go
