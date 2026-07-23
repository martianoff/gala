package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/internal/parser/grammar"
)

// tryTransformSelfTailRecursion rewrites direct self-tail-recursion in an
// expression-bodied function whose body is an if-expression into a `for {}`
// loop, so deep recursion runs in constant stack space.
//
// Given a function like:
//
//	func factorial(n int, acc int) int = if (n <= 0) acc else factorial(n - 1, n * acc)
//
// it produces the semantic equivalent of:
//
//	func factorial(n int, acc int) int {
//		for {
//			if n <= 0 {
//				return acc
//			} else {
//				_tailArg0 := n - 1
//				_tailArg1 := n * acc
//				n, acc = _tailArg0, _tailArg1
//				continue
//			}
//		}
//	}
//
// The tail self-call's argument expressions are all evaluated into temporaries
// using the OLD parameter values before any parameter is reassigned, so a call
// like factorial(n-1, n*acc) reads the original n for both arguments.
//
// It returns (loopBody, true, nil) when the rewrite applies. It returns
// (nil, false, nil) when the function does not match the supported pattern —
// the caller then falls back to the normal lowering, leaving behavior
// unchanged. Only a genuine tail self-call (the entire branch value is the
// self-call) is rewritten; non-tail recursion such as `n * factorial(n-1)` is
// left untouched.
//
// This slice deliberately supports only plain functions (no receiver — the
// caller must guard on that) whose body is an if-expression with
// single-expression branches. Receiver/generic-method forms, match-form and
// block-body forms, and mutual recursion are not handled here and fall through
// to the normal lowering.
func (t *galaASTTransformer) tryTransformSelfTailRecursion(
	exprCtx grammar.IExpressionContext,
	funcName string,
	funcType *ast.FuncType,
) (*ast.BlockStmt, bool, error) {
	// The body must be a bare if-expression.
	ifExprCtx := t.findIfExpressionInExpression(exprCtx)
	if ifExprCtx == nil {
		return nil, false, nil
	}

	// Collect parameter names/types. Only simple, non-variadic, single-name
	// parameters are supported; anything else disables the rewrite.
	if funcType.Params == nil {
		return nil, false, nil
	}
	var paramNames []string
	var paramTypes []ast.Expr
	for _, f := range funcType.Params.List {
		if len(f.Names) != 1 {
			return nil, false, nil
		}
		if _, isVariadic := f.Type.(*ast.Ellipsis); isVariadic {
			return nil, false, nil
		}
		paramNames = append(paramNames, f.Names[0].Name)
		paramTypes = append(paramTypes, f.Type)
	}
	if len(paramNames) == 0 {
		return nil, false, nil
	}

	var retType ast.Expr
	if funcType.Results != nil && len(funcType.Results.List) > 0 {
		retType = funcType.Results.List[0].Type
	}

	// Thread the declared return type into branch lowering so nested
	// if-expressions and lambdas in terminal branches infer correctly, matching
	// transformExpressionBodiedFunction.
	oldExpected := t.expectedIfExprType
	t.expectedIfExprType = retType
	defer func() { t.expectedIfExprType = oldExpected }()

	ifStmt, found, err := t.buildTailIfStmt(ifExprCtx, funcName, paramNames, paramTypes, retType)
	if err != nil {
		return nil, false, err
	}
	// No tail self-call anywhere: leave the function to the normal lowering so
	// non-recursive functions keep their existing output.
	if !found {
		return nil, false, nil
	}

	loop := &ast.ForStmt{
		Body: &ast.BlockStmt{List: []ast.Stmt{ifStmt}},
	}
	return &ast.BlockStmt{List: []ast.Stmt{loop}}, true, nil
}

// buildTailIfStmt lowers an if-expression into an *ast.IfStmt whose branches are
// either tail-call loop steps, terminal returns, or further nested if
// statements. The bool reports whether a tail self-call was found anywhere in
// the tree.
func (t *galaASTTransformer) buildTailIfStmt(
	ifExprCtx *grammar.IfExpressionContext,
	funcName string,
	paramNames []string,
	paramTypes []ast.Expr,
	retType ast.Expr,
) (*ast.IfStmt, bool, error) {
	cond, err := t.transformExpression(ifExprCtx.Expression())
	if err != nil {
		return nil, false, err
	}

	branches := ifExprCtx.AllIfExprBranch()
	if len(branches) != 2 {
		return nil, false, fmt.Errorf("tco: expected exactly two if-expression branches, got %d", len(branches))
	}

	thenBody, thenFound, err := t.buildTailBranch(branches[0].(*grammar.IfExprBranchContext), funcName, paramNames, paramTypes, retType)
	if err != nil {
		return nil, false, err
	}
	elseBody, elseFound, err := t.buildTailBranch(branches[1].(*grammar.IfExprBranchContext), funcName, paramNames, paramTypes, retType)
	if err != nil {
		return nil, false, err
	}

	return &ast.IfStmt{
		Cond: cond,
		Body: &ast.BlockStmt{List: thenBody},
		Else: &ast.BlockStmt{List: elseBody},
	}, thenFound || elseFound, nil
}

// buildTailBranch lowers a single if-expression branch. A single-expression
// branch is inspected for (a) a nested if-expression, (b) a direct tail
// self-call, or (c) a terminal value that becomes a `return`. Block branches
// are treated as terminal returns via the shared branch transform.
func (t *galaASTTransformer) buildTailBranch(
	branchCtx *grammar.IfExprBranchContext,
	funcName string,
	paramNames []string,
	paramTypes []ast.Expr,
	retType ast.Expr,
) ([]ast.Stmt, bool, error) {
	if exprCtx := branchCtx.Expression(); exprCtx != nil {
		// Nested if-expression: recurse so `if a x else if b self(...) else y`
		// keeps the tail self-call in tail position.
		if nested := t.findIfExpressionInExpression(exprCtx); nested != nil {
			ifStmt, found, err := t.buildTailIfStmt(nested, funcName, paramNames, paramTypes, retType)
			if err != nil {
				return nil, false, err
			}
			return []ast.Stmt{ifStmt}, found, nil
		}

		// Direct tail self-call.
		if stmts, ok, err := t.buildTailCallStep(exprCtx, funcName, paramNames, paramTypes); err != nil {
			return nil, false, err
		} else if ok {
			return stmts, true, nil
		}

		// Terminal value: return it.
		e, err := t.transformExpression(exprCtx)
		if err != nil {
			return nil, false, err
		}
		e = t.wrapWithAssertion(e, retType)
		return []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{e}}}, false, nil
	}

	// Block branch: reuse the shared branch transform and append a terminal
	// return when it does not already terminate. Tail self-calls hidden inside
	// block branches are not detected in this slice.
	stmts, expr, terminates, err := t.transformIfExprBranch(branchCtx)
	if err != nil {
		return nil, false, err
	}
	if !terminates {
		stmts = append(stmts, &ast.ReturnStmt{Results: []ast.Expr{t.wrapWithAssertion(expr, retType)}})
	}
	return stmts, false, nil
}

// buildTailCallStep detects whether exprCtx is a direct call to funcName with an
// argument count matching the parameters, and if so emits the loop-step
// statements: evaluate every argument into a temporary using the OLD parameter
// values, assign the temporaries back to the parameters simultaneously, then
// `continue`. Returns ok=false (with no statements) when exprCtx is not a
// supported tail self-call, so the caller can treat it as a terminal value.
func (t *galaASTTransformer) buildTailCallStep(
	exprCtx grammar.IExpressionContext,
	funcName string,
	paramNames []string,
	paramTypes []ast.Expr,
) ([]ast.Stmt, bool, error) {
	primaryExpr, argList, typeArgs := t.getCallPatternWithTypeArgsFromExpression(exprCtx)
	if primaryExpr == nil {
		return nil, false, nil
	}
	// Explicit type arguments (self[T](...)) are not supported in this slice.
	if typeArgs != nil {
		return nil, false, nil
	}
	// The call target must be exactly the enclosing function's (un-mangled) name.
	if primaryExpr.GetText() != funcName {
		return nil, false, nil
	}

	var args []grammar.IArgumentContext
	if argList != nil {
		args = argList.AllArgument()
	}
	if len(args) != len(paramNames) {
		return nil, false, nil
	}

	tmpNames := make([]string, len(args))
	var stmts []ast.Stmt
	for i, a := range args {
		argCtx := a.(*grammar.ArgumentContext)
		// Named arguments (name = expr) are not supported for the rewrite.
		if argCtx.Identifier() != nil {
			return nil, false, nil
		}
		exprC, lambdaC, isSpread, err := extractArgContent(argCtx)
		if err != nil {
			return nil, false, err
		}
		// Only plain positional expression arguments are supported.
		if lambdaC != nil || isSpread || exprC == nil {
			return nil, false, nil
		}
		e, err := t.transformExpression(exprC)
		if err != nil {
			return nil, false, err
		}
		e = t.wrapWithAssertion(e, paramTypes[i])
		tmp := fmt.Sprintf("_tailArg%d", i)
		tmpNames[i] = tmp
		stmts = append(stmts, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(tmp)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{e},
		})
	}

	// Simultaneous reassignment of every parameter from its temporary.
	lhs := make([]ast.Expr, len(paramNames))
	rhs := make([]ast.Expr, len(paramNames))
	for i := range paramNames {
		lhs[i] = ast.NewIdent(paramNames[i])
		rhs[i] = ast.NewIdent(tmpNames[i])
	}
	stmts = append(stmts, &ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, Rhs: rhs})
	stmts = append(stmts, &ast.BranchStmt{Tok: token.CONTINUE})
	return stmts, true, nil
}
