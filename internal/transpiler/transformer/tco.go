package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/internal/parser/grammar"
)

// NOTE (deferred forms): this slice detects tail self-calls on the ANTLR parse
// tree, before the normal lowering wraps an if-expression body in an IIFE (a
// `return self(...)` inside that closure would return from the closure, not the
// function, so a Go-AST post-pass would not see the tail call). match-form and
// block-form bodies also lower to IIFEs, so extending TCO to them via this
// approach means a parallel parse-tree matcher per form. Before adding the next
// form, decide whether to keep parallel matchers or pivot to a shared
// "expression body in tail-return position lowers to statements, then one
// Go-AST tail-call pass" so if/match/block are covered uniformly.

// tailCtx bundles the values that stay invariant across a single function's
// tail-recursion rewrite: the (un-mangled) self-call target name, the parameter
// names/types to reassign, and the declared return type used for assertions.
type tailCtx struct {
	funcName   string
	paramNames []string
	paramTypes []ast.Expr
	retType    ast.Expr
}

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
//				_tmp_1 := n - 1   // temp names come from the transformer's gensym
//				_tmp_2 := n * acc
//				n, acc = _tmp_1, _tmp_2
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
// single-expression branches. A tail self-call hidden inside a *block* branch
// (`if (c) { self(...) } else acc`) is not detected. Receiver/generic-method
// forms, match-form and block-body forms, and mutual recursion are not handled
// here and fall through to the normal lowering.
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
	paramNames := make([]string, 0, len(funcType.Params.List))
	paramTypes := make([]ast.Expr, 0, len(funcType.Params.List))
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

	// Cheap syntactic gate: only pay for the full branch lowering when a tail
	// self-call actually exists. Without this, every if-expression-bodied plain
	// function — the common case in a functional language — would have its
	// condition and terminal branches transformed here and then thrown away when
	// no tail call is found, only to be transformed a second time by the normal
	// lowering fallback. hasTailSelfCall walks the parse tree without
	// transforming anything.
	if !t.hasTailSelfCall(ifExprCtx, funcName, len(paramNames)) {
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

	tc := &tailCtx{funcName: funcName, paramNames: paramNames, paramTypes: paramTypes, retType: retType}

	ifStmt, found, err := t.buildTailIfStmt(ifExprCtx, tc)
	if err != nil {
		return nil, false, err
	}
	// Defensive: the syntactic gate said a tail self-call exists, but the full
	// pass can still reject it (e.g. named/spread args) — fall back cleanly.
	if !found {
		return nil, false, nil
	}

	loop := &ast.ForStmt{
		Body: &ast.BlockStmt{List: []ast.Stmt{ifStmt}},
	}
	return &ast.BlockStmt{List: []ast.Stmt{loop}}, true, nil
}

// hasTailSelfCall reports — without transforming anything — whether the
// if-expression has a call to funcName with arity argc in tail position. It
// mirrors the tail positions buildTailBranch rewrites (single-expression
// branches and nested if-expressions; block branches are not considered), so it
// is a conservative gate: true means "attempt the rewrite," false means "there
// is definitely no tail self-call to rewrite."
func (t *galaASTTransformer) hasTailSelfCall(ifExprCtx *grammar.IfExpressionContext, funcName string, argc int) bool {
	branches := ifExprCtx.AllIfExprBranch()
	if len(branches) != 2 {
		return false
	}
	for _, b := range branches {
		if t.branchHasTailSelfCall(b.(*grammar.IfExprBranchContext), funcName, argc) {
			return true
		}
	}
	return false
}

// branchHasTailSelfCall is the per-branch half of hasTailSelfCall.
func (t *galaASTTransformer) branchHasTailSelfCall(branchCtx *grammar.IfExprBranchContext, funcName string, argc int) bool {
	exprCtx := branchCtx.Expression()
	if exprCtx == nil {
		return false // block branch: not detected in this slice
	}
	if nested := t.findIfExpressionInExpression(exprCtx); nested != nil {
		return t.hasTailSelfCall(nested, funcName, argc)
	}
	primaryExpr, argList, typeArgs := t.getCallPatternWithTypeArgsFromExpression(exprCtx)
	if primaryExpr == nil || typeArgs != nil {
		return false
	}
	if primaryExpr.GetText() != funcName {
		return false
	}
	nargs := 0
	if argList != nil {
		nargs = len(argList.AllArgument())
	}
	return nargs == argc
}

// buildTailIfStmt lowers an if-expression into an *ast.IfStmt whose branches are
// either tail-call loop steps, terminal returns, or further nested if
// statements. The bool reports whether a tail self-call was found anywhere in
// the tree.
func (t *galaASTTransformer) buildTailIfStmt(
	ifExprCtx *grammar.IfExpressionContext,
	tc *tailCtx,
) (*ast.IfStmt, bool, error) {
	cond, err := t.transformExpression(ifExprCtx.Expression())
	if err != nil {
		return nil, false, err
	}

	branches := ifExprCtx.AllIfExprBranch()
	if len(branches) != 2 {
		return nil, false, fmt.Errorf("tco: expected exactly two if-expression branches, got %d", len(branches))
	}

	thenBody, thenFound, err := t.buildTailBranch(branches[0].(*grammar.IfExprBranchContext), tc)
	if err != nil {
		return nil, false, err
	}
	elseBody, elseFound, err := t.buildTailBranch(branches[1].(*grammar.IfExprBranchContext), tc)
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
	tc *tailCtx,
) ([]ast.Stmt, bool, error) {
	if exprCtx := branchCtx.Expression(); exprCtx != nil {
		// Nested if-expression: recurse so `if a x else if b self(...) else y`
		// keeps the tail self-call in tail position.
		if nested := t.findIfExpressionInExpression(exprCtx); nested != nil {
			ifStmt, found, err := t.buildTailIfStmt(nested, tc)
			if err != nil {
				return nil, false, err
			}
			return []ast.Stmt{ifStmt}, found, nil
		}

		// Direct tail self-call.
		if stmts, ok, err := t.buildTailCallStep(exprCtx, tc); err != nil {
			return nil, false, err
		} else if ok {
			return stmts, true, nil
		}

		// Terminal value: return it.
		e, err := t.transformExpression(exprCtx)
		if err != nil {
			return nil, false, err
		}
		e = t.wrapWithAssertion(e, tc.retType)
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
		stmts = append(stmts, &ast.ReturnStmt{Results: []ast.Expr{t.wrapWithAssertion(expr, tc.retType)}})
	}
	return stmts, false, nil
}

// buildTailCallStep detects whether exprCtx is a direct call to tc.funcName with
// an argument count matching the parameters, and if so emits the loop-step
// statements: evaluate every argument into a temporary using the OLD parameter
// values, assign the temporaries back to the parameters simultaneously, then
// `continue`. Returns ok=false (with no statements) when exprCtx is not a
// supported tail self-call, so the caller can treat it as a terminal value.
func (t *galaASTTransformer) buildTailCallStep(
	exprCtx grammar.IExpressionContext,
	tc *tailCtx,
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
	if primaryExpr.GetText() != tc.funcName {
		return nil, false, nil
	}

	var args []grammar.IArgumentContext
	if argList != nil {
		args = argList.AllArgument()
	}
	if len(args) != len(tc.paramNames) {
		return nil, false, nil
	}

	tmpNames := make([]string, len(args))
	stmts := make([]ast.Stmt, 0, len(args)+2)
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
		e = t.wrapWithAssertion(e, tc.paramTypes[i])
		// Use the package gensym so temps can't collide with a user identifier
		// and participate in the shared `_tmp_*` internal-temp convention.
		tmp := t.nextTempVar()
		tmpNames[i] = tmp
		stmts = append(stmts, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(tmp)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{e},
		})
	}

	// Simultaneous reassignment of every parameter from its temporary.
	lhs := make([]ast.Expr, len(tc.paramNames))
	rhs := make([]ast.Expr, len(tc.paramNames))
	for i := range tc.paramNames {
		lhs[i] = ast.NewIdent(tc.paramNames[i])
		rhs[i] = ast.NewIdent(tmpNames[i])
	}
	stmts = append(stmts, &ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, Rhs: rhs})
	stmts = append(stmts, &ast.BranchStmt{Tok: token.CONTINUE})
	return stmts, true, nil
}
