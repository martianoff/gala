package transformer

import (
	"go/ast"
	"go/token"
)

// NOTE (deferred forms): this slice detects tail self-calls on the parse tree
// (either tree: ifExprSrc, ifBranchSrc, callPat), before the normal lowering
// wraps an if-expression body in an IIFE (a `return self(...)` inside that
// closure would return from the closure, not the function, so a Go-AST
// post-pass would not see the tail call). match-form and
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

// tailResultSlot is the slot a terminal branch fills: the declared return
// type, as for transformExpressionBodiedFunction.
func (t *galaASTTransformer) tailResultSlot(tc *tailCtx) slot {
	if tc.retType == nil {
		return slot{}
	}
	return typedSlot(t.astTypeToTranspilerType(tc.retType))
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
	body exprSrc,
	funcName string,
	funcType *ast.FuncType,
) (*ast.BlockStmt, bool, error) {
	// The body must be a bare if-expression.
	ifExpr := t.ifIn(body)
	if !ifExpr.ok() {
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
	if !t.hasTailSelfCall(ifExpr, funcName, len(paramNames)) {
		return nil, false, nil
	}

	var retType ast.Expr
	if funcType.Results != nil && len(funcType.Results.List) > 0 {
		retType = funcType.Results.List[0].Type
	}

	// Terminal branches are lowered against the declared return type, matching
	// transformExpressionBodiedFunction.
	tc := &tailCtx{funcName: funcName, paramNames: paramNames, paramTypes: paramTypes, retType: retType}

	ifStmt, found, err := t.buildTailIfStmt(ifExpr, tc)
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
func (t *galaASTTransformer) hasTailSelfCall(ifExpr ifExprSrc, funcName string, argc int) bool {
	for _, b := range ifExpr.branches() {
		if t.branchHasTailSelfCall(b, funcName, argc) {
			return true
		}
	}
	return false
}

// branchHasTailSelfCall is the per-branch half of hasTailSelfCall.
func (t *galaASTTransformer) branchHasTailSelfCall(b ifBranchSrc, funcName string, argc int) bool {
	e := b.expr()
	if !e.ok() {
		return false // block branch: not detected in this slice
	}
	if nested := t.ifIn(e); nested.ok() {
		return t.hasTailSelfCall(nested, funcName, argc)
	}
	call, ok := selfCall(e, funcName)
	return ok && len(call.args.list) == argc
}

// selfCall is the call e is when it calls funcName by its bare name, with no
// type arguments, and false when e is anything else. A call with a match on
// its result (`f(args) match { ... }`) is not one: the match, not the call, is
// in tail position.
func selfCall(e exprSrc, funcName string) (callPat, bool) {
	call, ok := patExpr{exprSrc: e}.callPattern()
	if !ok || call.head.hasCases() || call.hasTypeArgList() || call.head.primaryIdent() != funcName {
		return callPat{}, false
	}
	return call, true
}

// buildTailIfStmt lowers an if-expression into an *ast.IfStmt whose branches are
// either tail-call loop steps, terminal returns, or further nested if
// statements. The bool reports whether a tail self-call was found anywhere in
// the tree.
func (t *galaASTTransformer) buildTailIfStmt(
	ifExpr ifExprSrc,
	tc *tailCtx,
) (*ast.IfStmt, bool, error) {
	cond, err := t.lowerExpr(ifExpr.cond())
	if err != nil {
		return nil, false, err
	}

	branches := ifExpr.branches()
	thenBody, thenFound, err := t.buildTailBranch(branches[0], tc)
	if err != nil {
		return nil, false, err
	}
	elseBody, elseFound, err := t.buildTailBranch(branches[1], tc)
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
	branch ifBranchSrc,
	tc *tailCtx,
) ([]ast.Stmt, bool, error) {
	if e := branch.expr(); e.ok() {
		// Nested if-expression: recurse so `if a x else if b self(...) else y`
		// keeps the tail self-call in tail position.
		if nested := t.ifIn(e); nested.ok() {
			ifStmt, found, err := t.buildTailIfStmt(nested, tc)
			if err != nil {
				return nil, false, err
			}
			return []ast.Stmt{ifStmt}, found, nil
		}

		// Direct tail self-call.
		if stmts, ok, err := t.buildTailCallStep(e, tc); err != nil {
			return nil, false, err
		} else if ok {
			return stmts, true, nil
		}

		// Terminal value: return it.
		value, err := t.lowerResultAgainst(e, t.tailResultSlot(tc), false)
		if err != nil {
			return nil, false, err
		}
		value = t.wrapWithAssertion(value, tc.retType)
		return []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{value}}}, false, nil
	}

	// Block branch: reuse the shared branch transform and append a terminal
	// return when it does not already terminate. Tail self-calls hidden inside
	// block branches are not detected in this slice.
	b, err := t.lowerIfExprBranch(branch, t.tailResultSlot(tc))
	if err != nil {
		return nil, false, err
	}
	if b.noValue && !t.leaves(b.stmts) {
		return nil, false, t.noValueBranchError(branch.at())
	}
	if b.needsValueReturn() {
		return append(b.stmts, &ast.ReturnStmt{Results: []ast.Expr{t.wrapWithAssertion(b.expr, tc.retType)}}), false, nil
	}
	return b.stmts, false, nil
}

// buildTailCallStep detects whether e is a direct call to tc.funcName with an
// argument count matching the parameters, and if so emits the loop-step
// statements: evaluate every argument into a temporary using the OLD parameter
// values, assign the temporaries back to the parameters simultaneously, then
// `continue`. Returns ok=false (with no statements) when e is not a supported
// tail self-call, so the caller can treat it as a terminal value.
func (t *galaASTTransformer) buildTailCallStep(
	e exprSrc,
	tc *tailCtx,
) ([]ast.Stmt, bool, error) {
	// The call target must be exactly the enclosing function's (un-mangled)
	// name. Explicit type arguments (self[T](...)) are not supported in this
	// slice.
	call, ok := selfCall(e, tc.funcName)
	if !ok {
		return nil, false, nil
	}
	args := call.args.list
	if len(args) != len(tc.paramNames) {
		return nil, false, nil
	}

	tmpNames := make([]string, len(args))
	stmts := make([]ast.Stmt, 0, len(args)+2)
	for i, a := range args {
		// Named arguments (name = expr) are not supported for the rewrite.
		if a.named() {
			return nil, false, nil
		}
		v, err := t.argContent(a)
		if err != nil {
			return nil, false, err
		}
		// Only plain positional expression arguments are supported.
		if v.lambda.ok() || v.spread || !v.expr.ok() {
			return nil, false, nil
		}
		t.nativeShape(v.expr.node, "tail call")
		arg, err := t.lowerExpr(v.expr)
		if err != nil {
			return nil, false, err
		}
		// The argument rebinds the parameter: a nil, or a pointer that may be
		// nil, never fills a Ptr parameter.
		if err := t.checkPtrSlot(arg, t.astTypeToTranspilerType(tc.paramTypes[i]), v.expr.at()); err != nil {
			return nil, false, err
		}
		arg = t.wrapWithAssertion(arg, tc.paramTypes[i])
		// A closure inside a tail-call argument would capture the loop-carried
		// parameter by reference; once that parameter is reassigned on the next
		// iteration, the escaped closure observes the mutated value — diverging
		// from the per-call binding the original recursion gives each frame.
		// Bail to the normal lowering (real recursion) for this case.
		if containsFuncLit(arg) {
			return nil, false, nil
		}
		// Use the package gensym so temps can't collide with a user identifier
		// and participate in the shared `_tmp_*` internal-temp convention.
		tmp := t.nextTempVar()
		tmpNames[i] = tmp
		stmts = append(stmts, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(tmp)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{arg},
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

// containsFuncLit reports whether e contains a function literal (closure)
// anywhere in its subtree. A closure in a tail-call argument captures the
// loop-carried parameters by reference, which the loop rewrite mutates between
// iterations — so its presence disqualifies the tail call from the rewrite.
func containsFuncLit(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			found = true
			return false
		}
		return !found
	})
	return found
}
