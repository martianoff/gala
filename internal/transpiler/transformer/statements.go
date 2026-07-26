package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

func (t *galaASTTransformer) transformSimpleStatement(ctx grammar.ISimpleStatementContext) (ast.Stmt, error) {
	return t.transformSimpleStatementWithMutability(ctx, false)
}

// transformForLoopInitStatement transforms a simple statement in a for loop init context.
// Variables declared with := in this context are mutable (can be incremented/decremented).
func (t *galaASTTransformer) transformForLoopInitStatement(ctx grammar.ISimpleStatementContext) (ast.Stmt, error) {
	return t.transformSimpleStatementWithMutability(ctx, true)
}

func (t *galaASTTransformer) transformSimpleStatementWithMutability(ctx grammar.ISimpleStatementContext, mutable bool) (ast.Stmt, error) {
	if incDecCtx := ctx.IncDecStmt(); incDecCtx != nil {
		return t.transformIncDecStmt(incDecCtx.(*grammar.IncDecStmtContext))
	}
	if assignCtx := ctx.Assignment(); assignCtx != nil {
		return t.transformAssignment(assignCtx.(*grammar.AssignmentContext))
	}
	if shortCtx := ctx.ShortVarDecl(); shortCtx != nil {
		return t.transformShortVarDeclWithMutability(shortCtx.(*grammar.ShortVarDeclContext), mutable)
	}
	if exprCtx := ctx.Expression(); exprCtx != nil {
		if err := t.checkForbiddenStatementKeyword(exprCtx); err != nil {
			return nil, err
		}
		expr, err := t.transformExpression(exprCtx)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	}
	return nil, nil
}

// forbiddenStatementKeywordSuggestions maps each Go-only statement keyword that
// GALA does NOT have in its grammar to an actionable replacement. None of these
// are GALA keywords, so the parser accepts them as a bare identifier
// expression-statement; they only ever produced working Go by accident of the
// final gofmt pass, which re-absorbs the following statement into a real Go
// DeferStmt/GoStmt/etc. (`defer` + `f.Close()` glue into one DeferStmt). That is
// undocumented, ungrammatical, and fragile, so a bare use is a hard error.
var forbiddenStatementKeywordSuggestions = map[string]string{
	"defer":       "GALA has no `defer`; use the `resource` combinators — `Using` / `Bracket` / `WithLock` from martianoff/gala/resource (or a `use x = ...` binding) — which guarantee cleanup on every exit path",
	"go":          "GALA has no bare `go` statement; use `go_interop.Spawn(() => ...)` to start a goroutine",
	"goto":        "GALA has no `goto`; use structured control flow — pattern matching, recursion, or a `for` loop",
	"fallthrough": "GALA has no `fallthrough`; `match` arms never fall through — combine patterns with `|` or restructure the match",
	"select":      "GALA has no `select` statement; use the go_interop channel helpers",
	"chan":        "GALA has no bare `chan` statement; use the go_interop channel helpers to build and operate on channels",
}

// checkForbiddenStatementKeyword rejects a bare Go-only statement keyword
// (`defer`, `go`, `goto`, `fallthrough`, `select`, `chan`) that the parser
// accepted as a lone identifier expression-statement. Such statements only
// "work" as an accident of the final gofmt round-trip (see
// forbiddenStatementKeywordSuggestions); GALA has native replacements, so this
// is a hard error (GALA-E0036).
//
// It is resolver-aware, mirroring checkForbiddenGoBuiltinCall: the name is only
// forbidden when it is a bare identifier that does NOT resolve to a
// user-defined function, a local binding (val/var/param), or a declared
// type/struct — so a program that legitimately named something after one of
// these words is left untouched.
func (t *galaASTTransformer) checkForbiddenStatementKeyword(exprCtx grammar.IExpressionContext) error {
	// Only a bare identifier statement (`defer`) is the accident; anything with
	// a postfix (`x.defer()`), operator, or arguments is a normal expression.
	if !t.isDirectVariableExpression(exprCtx) {
		return nil
	}
	pc := t.getPrimaryFromExpression(exprCtx)
	if pc == nil || pc.Identifier() == nil {
		return nil
	}
	name := pc.Identifier().GetText()
	suggestion, isKeyword := forbiddenStatementKeywordSuggestions[name]
	if !isKeyword {
		return nil
	}
	// Resolver-aware guards: a name the author actually declared is that
	// declaration, not the leaked Go keyword.
	if t.getFunction(name) != nil {
		return nil
	}
	if !t.getType(name).IsNil() {
		return nil
	}
	if t.getTypeMeta(name) != nil {
		return nil
	}
	if _, ok := t.structFields[name]; ok {
		return nil
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeForbiddenStatementKeyword,
		exprCtx.GetStart().GetLine(), exprCtx.GetStart().GetColumn(),
		fmt.Sprintf("bare Go statement keyword %q is not part of GALA's surface", name),
		suggestion,
	)
}

func (t *galaASTTransformer) transformIncDecStmt(ctx *grammar.IncDecStmtContext) (ast.Stmt, error) {
	expr, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}

	// Check for mutability - get the name if it's an identifier
	if ident, ok := expr.(*ast.Ident); ok {
		if t.isVal(ident.Name) {
			return nil, t.semanticErrorAt(ctx, fmt.Sprintf("cannot increment/decrement immutable variable %s", ident.Name))
		}
	}

	// Determine the token (++ or --)
	tok := token.INC
	if ctx.GetChildCount() >= 2 {
		if termNode, ok := ctx.GetChild(1).(antlr.TerminalNode); ok {
			if termNode.GetText() == "--" {
				tok = token.DEC
			}
		}
	}

	return &ast.IncDecStmt{
		X:   expr,
		Tok: tok,
	}, nil
}

func (t *galaASTTransformer) transformStatement(ctx *grammar.StatementContext) (ast.Stmt, error) {
	if declCtx := ctx.Declaration(); declCtx != nil {
		decl, stmt, err := t.transformDeclaration(declCtx)
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			return stmt, nil
		}
		if decl != nil {
			return &ast.DeclStmt{Decl: decl}, nil
		}
		return nil, nil
	}
	if retCtx := ctx.ReturnStatement(); retCtx != nil {
		var results []ast.Expr
		if retCtx.Expression() != nil {
			var expr ast.Expr
			var err error
			// If the return expression is an if-expression and we know the
			// enclosing function's return type, set expectedIfExprType so that the
			// if-expression IIFE gets a concrete return type instead of falling back
			// to `any` when HM type inference fails in multi-file batch mode.
			ifExprCtx := t.findIfExpressionInExpression(retCtx.Expression())
			// If the return expression is a bare lambda and the enclosing
			// function returns a function type, propagate the expected param
			// and return types into the lambda. This mirrors what
			// transformExpressionBodiedFunction does for `func f() T = lambda`,
			// without which the lambda's untyped parameters fall through as
			// `any` and downstream match expressions that scrutinize them
			// erase their generic type arguments (e.g. `Try[Msg]` → `Try[any]`).
			lambdaCtx := t.findLambdaInExpression(retCtx.Expression())
			if lambdaCtx != nil && t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() {
				if expectedFuncType := t.resolveTranspilerTypeAsFuncType(t.currentFuncReturnType); expectedFuncType != nil {
					var expectedRetType ast.Expr
					if len(expectedFuncType.Results) > 0 {
						expectedRetType = t.typeToExpr(expectedFuncType.Results[0])
					} else {
						expectedRetType = ExpectedVoid
					}
					expr, err = t.transformLambdaWithExpectedType(lambdaCtx, expectedRetType, expectedFuncType.Params, false)
				}
			}
			if expr == nil && err == nil {
				if ifExprCtx != nil && t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() {
					oldExpected := t.expectedIfExprType
					t.expectedIfExprType = t.typeToExpr(t.currentFuncReturnType)
					expr, err = t.transformIfExpression(ifExprCtx)
					t.expectedIfExprType = oldExpected
				} else {
					expr, err = t.transformExpression(retCtx.Expression())
				}
			}
			if err != nil {
				return nil, err
			}
			results = append(results, t.unwrapImmutable(expr))
		}
		return &ast.ReturnStmt{Results: results}, nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformAssignment(ctx *grammar.AssignmentContext) (ast.Stmt, error) {
	lhsCtx := ctx.GetChild(0).(*grammar.ExpressionListContext)
	for _, exprCtx := range lhsCtx.AllExpression() {
		if pc := t.getPrimaryFromExpression(exprCtx); pc != nil {
			if pc.Identifier() != nil {
				name := pc.Identifier().GetText()
				// Only block direct variable reassignment (e.g., v = ...), not field/index
				// access through a val binding (e.g., v.data = ..., v[i] = ...).
				// Field access assignments are checked by the field immutability check below.
				// For value types (non-pointer), Go compiler itself catches invalid mutations.
				if t.isVal(name) && t.isDirectVariableExpression(exprCtx) {
					return nil, t.semanticErrorAt(ctx, fmt.Sprintf("cannot assign to immutable variable %s", name))
				}
			}
		}
		// Check for dereference assignment (*ptr = value) where ptr is ConstPtr
		if t.isConstPtrDerefAssignment(exprCtx) {
			return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "cannot assign through ConstPtr - read-only pointer to immutable value")
		}
		if exprCtx.GetChildCount() == 3 && exprCtx.GetChild(1).(antlr.ParseTree).GetText() == "." {
			selName := exprCtx.GetChild(2).(antlr.ParseTree).GetText()
			xExpr, err := t.transformExpression(exprCtx.GetChild(0).(grammar.IExpressionContext))
			if err == nil {
				typeName := t.getExprTypeName(xExpr).String()
				baseTypeName := stripTypeNameDecorations(typeName)

				resolvedTypeName := t.resolveStructTypeName(baseTypeName)
				if fields, ok := t.structFields[resolvedTypeName]; ok {
					for i, f := range fields {
						if f == selName {
							if t.structImmutFields[resolvedTypeName][i] {
								return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), fmt.Sprintf("cannot assign to immutable field %s", selName))
							}
							break
						}
					}
				}
			}
		}
	}

	lhsExprs, err := t.transformExpressionList(lhsCtx)
	if err != nil {
		return nil, err
	}

	// Downward type-inference for sealed-variant constructors on the RHS:
	// when the LHS is a single bare variable (`failure = Some(...)`) and that
	// variable's declared type carries concrete type arguments
	// (e.g. `Option[string]`), push the LHS type onto the expected-type stack
	// so the RHS call dispatcher can pick up the parent sealed type's type
	// arguments and emit `Some[string]{}.Apply(...)` instead of an
	// uninstantiated `Some{}.Apply(...)`. Mirrors the same hint pushed by val
	// declarations with explicit type annotations (declarations.go).
	rhsListCtx := ctx.GetChild(2).(*grammar.ExpressionListContext)
	if lhsName, lhsOk := t.singleAssignmentLHSName(lhsCtx); lhsOk {
		if lhsType := t.getValType(lhsName); !lhsType.IsNil() {
			release := t.expectedArgTypes.push(lhsType)
			defer release()
		}
	}
	rhsExprs, err := t.transformExpressionList(rhsListCtx)
	if err != nil {
		return nil, err
	}

	unwrappedRhs := make([]ast.Expr, len(rhsExprs))
	for i, r := range rhsExprs {
		unwrappedRhs[i] = t.unwrapImmutable(r)
	}

	op := ctx.GetChild(1).(antlr.TerminalNode).GetText()
	var tok token.Token
	switch op {
	case "=":
		tok = token.ASSIGN
	case "+=":
		tok = token.ADD_ASSIGN
	case "-=":
		tok = token.SUB_ASSIGN
	case "*=":
		tok = token.MUL_ASSIGN
	case "/=":
		tok = token.QUO_ASSIGN
	default:
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), fmt.Sprintf("unknown assignment operator: %s", op))
	}

	return &ast.AssignStmt{
		Lhs: lhsExprs,
		Tok: tok,
		Rhs: unwrappedRhs,
	}, nil
}

func (t *galaASTTransformer) transformShortVarDecl(ctx *grammar.ShortVarDeclContext) (ast.Stmt, error) {
	return t.transformShortVarDeclWithMutability(ctx, false)
}

func (t *galaASTTransformer) transformShortVarDeclWithMutability(ctx *grammar.ShortVarDeclContext, mutable bool) (ast.Stmt, error) {
	idsCtx := ctx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()
	rhsExprs, err := t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	lhs := make([]ast.Expr, 0)
	rhs := make([]ast.Expr, 0)
	for i, idCtx := range idsCtx {
		name := idCtx.GetText()
		typeName := t.getExprTypeName(rhsExprs[i])
		if qName := t.getType(typeName.String()); !qName.IsNil() {
			typeName = qName
		}
		if mutable {
			t.addVar(name, typeName)
			t.markMutable(name)
		} else {
			t.addVal(name, typeName)
		}
		lhs = append(lhs, ast.NewIdent(name))

		var val ast.Expr
		if i < len(rhsExprs) {
			val = t.unwrapImmutable(rhsExprs[i])
		} else {
			val = &ast.IndexExpr{X: t.unwrapImmutable(rhsExprs[0]), Index: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)}}
		}

		// Auto-destructure Go functions returning (T, error)
		val = t.wrapGoMultiReturnAsIIFE(val)

		if t.isNoneCall(val) {
			return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "variable assigned to None() must have an explicit type")
		}

		if mutable {
			// For mutable variables (e.g., for loop init), don't wrap in Immutable
			rhs = append(rhs, val)
		} else {
			rhs = append(rhs, &ast.CallExpr{
				Fun:  t.stdIdent("NewImmutable"),
				Args: []ast.Expr{val},
			})
		}
	}

	return &ast.AssignStmt{
		Lhs: lhs,
		Tok: token.DEFINE,
		Rhs: rhs,
	}, nil
}

func (t *galaASTTransformer) transformBlock(ctx *grammar.BlockContext) (*ast.BlockStmt, error) {
	t.pushScope()
	defer t.popScope()
	// Capture and reset the block-last-stmt-is-value flag once: it applies
	// only to *this* block's last statement, not to any nested blocks.
	lastStmtIsValue := t.blockLastStmtIsValue
	t.blockLastStmtIsValue = false
	defer func() { t.blockLastStmtIsValue = lastStmtIsValue }()

	block := &ast.BlockStmt{}
	allStmts := ctx.AllStatement()
	lastIdx := len(allStmts) - 1
	for i, stmtCtx := range allStmts {
		// Source-mapped `//line` directive: mark each statement with its
		// originating GALA line so a panic reports the GALA position. The marker
		// is prepended before the statement's generated code — never appended —
		// so the block's trailing statement stays real and downstream
		// trailing-expression handling is unaffected (see line_directives.go).
		if t.emitLineMarkers() && stmtCtx.GetStart() != nil {
			block.List = append(block.List, lineMarkerStmt(stmtCtx.GetStart().GetLine()))
		}
		// Monadic do-notation: a `bind` collapses itself and every following
		// statement in the block into a FlatMap chain (see bind.go). Statements
		// before the first `bind` are emitted normally by prior iterations. An
		// `also` is only valid immediately following a `bind`/`also` (handled
		// inside the chain), so one reached here has no preceding `bind`.
		if alsoDeclFromStatement(stmtCtx) != nil {
			return nil, t.semanticErrorAt(stmtCtx.(*grammar.StatementContext), "`also` must follow a `bind`")
		}
		if bindDeclFromStatement(stmtCtx) != nil {
			if t.currentFuncReturnType == nil || t.currentFuncReturnType.IsNil() {
				return nil, t.semanticErrorAt(stmtCtx.(*grammar.StatementContext), "`bind` requires the enclosing function to declare a monad return type")
			}
			expr, err := t.desugarBindChain(allStmts[i:], t.currentFuncReturnType)
			if err != nil {
				return nil, err
			}
			if lastStmtIsValue {
				block.List = append(block.List, &ast.ReturnStmt{Results: []ast.Expr{expr}})
			} else {
				block.List = append(block.List, &ast.ExprStmt{X: expr})
			}
			return block, nil
		}
		// A `use x = acquire` scoped-resource binding lowers to `x := acquire`
		// plus `defer x.Close()`; the binding stays in scope for the rest of
		// this block and releases (LIFO) when the function returns. Emitted
		// inline so subsequent statements see `x`.
		if useDecl := useDeclFromStatement(stmtCtx); useDecl != nil {
			useStmts, err := t.transformUseDeclaration(useDecl)
			if err != nil {
				return nil, err
			}
			block.List = append(block.List, useStmts...)
			continue
		}
		// A bare `subject match { ... }` whose value is discarded by the
		// surrounding ExprStmt must be lowered as a void IIFE; otherwise
		// arms calling void Go functions (e.g. `d.Skip()`) get wrapped in
		// `return d.Skip()` because at least one other arm produced a typed
		// value, and Go rejects "return d.Skip()" as "no value used as
		// value". A non-trailing statement is unconditionally
		// statement-position; the trailing statement is statement-position
		// only when the caller did NOT signal that the block's last
		// expression is consumed (via blockLastStmtIsValue) — function
		// bodies with a return type, lambda bodies, and match arm bodies
		// all set that flag, since their trailing expression becomes the
		// block's value.
		prev := t.matchInStatementPos
		isTrailing := i == lastIdx
		discardsValue := !isTrailing || !lastStmtIsValue
		if discardsValue && stmtIsBareMatchExpression(stmtCtx.(*grammar.StatementContext), t) {
			t.matchInStatementPos = true
		}
		stmt, err := t.transformStatement(stmtCtx.(*grammar.StatementContext))
		t.matchInStatementPos = prev
		if err != nil {
			return nil, err
		}
		// A statement-position match with a user-written `return X` inside an
		// arm body cannot be lowered as an IIFE (the bare return that
		// stripReturnStatements emits only exits the synthetic lambda, leaving
		// any enclosing for-loop spinning forever). transformMatchExpression
		// detects this case, builds the body as an inlined block, and stores
		// it in pendingMatchStmtBlock; we replace the placeholder ExprStmt
		// with the inlined block here so the user's `return X` becomes a real
		// Go return from the enclosing function.
		if t.pendingMatchStmtBlock != nil {
			block.List = append(block.List, t.pendingMatchStmtBlock)
			t.pendingMatchStmtBlock = nil
			continue
		}
		block.List = append(block.List, stmt)
	}
	return block, nil
}

// transformUseDeclaration lowers a `use x = acquire` scoped-resource binding to
// the two Go statements that implement it: `x := acquire` and `defer x.Close()`.
// The resource is bound for the rest of the enclosing block and released — via
// its Close() method — when the function returns, on every path (normal or
// panic), LIFO with any other `use`/defer. This is the GALA-native, non-
// forgettable replacement for the (now-forbidden) bare Go `defer x.Close()`;
// emitting Go `defer` here is the sanctioned internal lowering — it lives in the
// generated Go, never on the GALA surface.
//
// The binding is registered with its concrete resource type (not wrapped in
// Immutable), so `x` and `x.Close()` read as plain Go and no `.Get()` unwrap is
// injected. Acquisition is a single-value expression; a fallible Go acquire that
// returns `(resource, error)` should be error-checked first (or use the
// resource combinators directly).
func (t *galaASTTransformer) transformUseDeclaration(ctx grammar.IUseDeclarationContext) ([]ast.Stmt, error) {
	name := ctx.Identifier().GetText()
	acquire, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	acquire = t.unwrapImmutable(acquire)

	// Register the binding with its concrete type so downstream references and
	// the Close() call resolve directly (no Immutable unwrapping). Prefer an
	// explicit annotation when present; otherwise infer from the acquire expr.
	resType := t.getExprTypeName(acquire)
	if typeCtx := ctx.Type_(); typeCtx != nil {
		if typeExpr, terr := t.transformType(typeCtx); terr == nil {
			if annotated := t.astTypeToTranspilerType(typeExpr); annotated != nil && !annotated.IsNil() {
				resType = annotated
			}
		}
	}
	// The binding holds the plain resource (`x := acquire`), not an Immutable
	// wrapper, so strip any inferred Immutable[T] to T and register it with
	// mutable-storage (addVar) semantics. A `val` binding is always
	// Immutable-wrapped, so every read of it is rewritten to `x.Get()`
	// (transformPrimary); `use` stores the resource directly, so its reads and
	// `x.Close()` must stay plain — addVar gives exactly that.
	if t.isImmutableType(resType) {
		if gen, ok := resType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
			resType = gen.Params[0]
		}
	}
	t.addVar(name, resType)

	assign := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(name)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{acquire},
	}
	deferStmt := &ast.DeferStmt{
		Call: &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: ast.NewIdent(name), Sel: ast.NewIdent("Close")},
		},
	}
	return []ast.Stmt{assign, deferStmt}, nil
}

// stmtIsBareMatchExpression reports whether a statement is just a bare
// `subject match { ... }` expression. It descends through statement →
// declaration / simpleStatement → expression to reach the match check.
// The transformer is passed for access to expressionIsBareMatch.
func stmtIsBareMatchExpression(ctx *grammar.StatementContext, t *galaASTTransformer) bool {
	if ctx == nil {
		return false
	}
	declCtx := ctx.Declaration()
	if declCtx == nil {
		return false
	}
	dc, ok := declCtx.(*grammar.DeclarationContext)
	if !ok {
		return false
	}
	simpleCtx := dc.SimpleStatement()
	if simpleCtx == nil {
		return false
	}
	sc, ok := simpleCtx.(*grammar.SimpleStatementContext)
	if !ok {
		return false
	}
	exprCtx := sc.Expression()
	if exprCtx == nil {
		return false
	}
	return t.expressionIsBareMatch(exprCtx)
}

func (t *galaASTTransformer) transformForStatement(ctx *grammar.ForStatementContext) (ast.Stmt, error) {
	// Handle condition-only for loop: for condition { ... }
	if condCtx := ctx.ForCondition(); condCtx != nil {
		cond, err := t.transformExpression(condCtx.(*grammar.ForConditionContext).Expression())
		if err != nil {
			return nil, err
		}
		// Parenthesize composite literals in for conditions.
		cond = parenthesizeCompositeLits(cond)
		body, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		return &ast.ForStmt{
			Cond: cond,
			Body: body,
		}, nil
	}

	// Handle range clause: for x := range expr
	if rangeCtx := ctx.RangeClause(); rangeCtx != nil {
		rangeClause := rangeCtx.(*grammar.RangeClauseContext)

		// Push scope for range variables - they should be visible in the body
		t.pushScope()
		defer t.popScope()

		// Transform the range expression
		rangeExpr, err := t.transformExpression(rangeClause.Expression())
		if err != nil {
			return nil, err
		}

		// Infer key/value types from range expression
		keyType, valueType := t.inferRangeTypes(rangeExpr)

		// Set up key and value identifiers
		var key, value ast.Expr
		if idListCtx := rangeClause.IdentifierList(); idListCtx != nil {
			ids := idListCtx.(*grammar.IdentifierListContext).AllIdentifier()
			if len(ids) >= 1 {
				keyName := ids[0].GetText()
				t.addVar(keyName, keyType)
				key = ast.NewIdent(keyName)
			}
			if len(ids) >= 2 {
				valueName := ids[1].GetText()
				t.addVar(valueName, valueType)
				value = ast.NewIdent(valueName)
			}
		}

		// Determine if using := or =
		tok := token.DEFINE
		if rangeClause.GetChildCount() > 1 {
			for i := 0; i < rangeClause.GetChildCount(); i++ {
				if child := rangeClause.GetChild(i); child != nil {
					if termNode, ok := child.(antlr.TerminalNode); ok && termNode.GetText() == "=" {
						tok = token.ASSIGN
						break
					}
				}
			}
		}

		// Transform body AFTER range variables are in scope
		body, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}

		return &ast.RangeStmt{
			Key:   key,
			Value: value,
			Tok:   tok,
			X:     rangeExpr,
			Body:  body,
		}, nil
	}

	// Handle for clause: for init; condition; post
	if forClauseCtx := ctx.ForClause(); forClauseCtx != nil {
		forClause := forClauseCtx.(*grammar.ForClauseContext)

		// Push scope for init variables - they should be visible in condition, post, and body
		t.pushScope()
		defer t.popScope()

		var init ast.Stmt
		var cond ast.Expr
		var post ast.Stmt
		var err error

		// Process init FIRST so variables are in scope for condition and body
		// Note: init uses transformForLoopInitStatement to make := declarations mutable
		simpleStmts := forClause.AllSimpleStatement()
		if len(simpleStmts) >= 1 && simpleStmts[0] != nil {
			init, err = t.transformForLoopInitStatement(simpleStmts[0].(*grammar.SimpleStatementContext))
			if err != nil {
				return nil, err
			}
		}

		// Process condition (can use init variables)
		if forClause.Expression() != nil {
			cond, err = t.transformExpression(forClause.Expression())
			if err != nil {
				return nil, err
			}
			// Parenthesize composite literals in for conditions.
			cond = parenthesizeCompositeLits(cond)
		}

		// Process post (can use init variables)
		if len(simpleStmts) >= 2 && simpleStmts[1] != nil {
			post, err = t.transformSimpleStatement(simpleStmts[1].(*grammar.SimpleStatementContext))
			if err != nil {
				return nil, err
			}
		}

		// Transform body LAST - after init variables are in scope
		body, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}

		return &ast.ForStmt{
			Init: init,
			Cond: cond,
			Post: post,
			Body: body,
		}, nil
	}

	// Infinite loop: for { ... }
	body, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
	if err != nil {
		return nil, err
	}
	return &ast.ForStmt{
		Body: body,
	}, nil
}

// isConstPtrDerefAssignment checks if the expression is a pointer dereference
// where the pointer type is ConstPtr. Such assignments are not allowed because
// ConstPtr provides read-only access to the pointed-to value.
func (t *galaASTTransformer) isConstPtrDerefAssignment(ctx grammar.IExpressionContext) bool {
	// Navigate through the expression structure to find unary expressions
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return false
	}
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) != 1 {
		return false
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) != 1 {
		return false
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) != 1 {
		return false
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) != 1 {
		return false
	}
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) != 1 {
		return false
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) != 1 {
		return false
	}
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)

	// Check if this is a dereference (*) operation
	if unaryOp := unaryCtx.UnaryOp(); unaryOp != nil {
		if unaryOp.GetText() == "*" {
			// Get the inner expression (the pointer being dereferenced)
			innerUnary := unaryCtx.UnaryExpr()
			if innerUnary != nil {
				innerExpr, err := t.transformUnaryExpr(innerUnary.(*grammar.UnaryExprContext))
				if err == nil {
					typeObj := t.getExprTypeName(innerExpr)
					return t.isConstPtrType(typeObj)
				}
			}
		}
	}
	return false
}

// singleAssignmentLHSName returns the bare variable name on the LHS of a
// single-target assignment (`x = ...`). Returns ("", false) for tuple-style
// assignments (`a, b = ...`), field/index targets (`v.f = ...`, `v[i] = ...`),
// and any other expression that is not a plain identifier.
//
// Used by transformAssignment to look up the variable's declared type so
// downward inference can drive sealed-variant constructors on the RHS.
func (t *galaASTTransformer) singleAssignmentLHSName(lhsCtx *grammar.ExpressionListContext) (string, bool) {
	exprs := lhsCtx.AllExpression()
	if len(exprs) != 1 {
		return "", false
	}
	exprCtx := exprs[0]
	if !t.isDirectVariableExpression(exprCtx) {
		return "", false
	}
	pc := t.getPrimaryFromExpression(exprCtx)
	if pc == nil || pc.Identifier() == nil {
		return "", false
	}
	return pc.Identifier().GetText(), true
}

// isDirectVariableExpression checks whether the expression is a bare identifier
// with no postfix operations (field access, indexing, or method calls).
// Returns true for `v`, false for `v.data`, `v[i]`, `v.Method()`, etc.
func (t *galaASTTransformer) isDirectVariableExpression(ctx grammar.IExpressionContext) bool {
	if ctx == nil {
		return false
	}
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return false
	}
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) != 1 {
		return false
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) != 1 {
		return false
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) != 1 {
		return false
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) != 1 {
		return false
	}
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) != 1 {
		return false
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) != 1 {
		return false
	}
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return false
	}
	// A direct variable expression has no postfix suffixes (no .field, [index], or (args))
	return len(postfixExpr.(*grammar.PostfixExprContext).AllPostfixSuffix()) == 0
}

func (t *galaASTTransformer) transformIfStatement(ctx *grammar.IfStatementContext) (ast.Stmt, error) {
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	// Parenthesize composite literals in if conditions to avoid
	// Go parser ambiguity where '{' is treated as the start of the if body.
	cond = parenthesizeCompositeLits(cond)
	body, err := t.transformBlock(ctx.Block(0).(*grammar.BlockContext))
	if err != nil {
		return nil, err
	}
	stmt := &ast.IfStmt{
		Cond: cond,
		Body: body,
	}

	if ctx.SimpleStatement() != nil {
		init, err := t.transformSimpleStatement(ctx.SimpleStatement().(*grammar.SimpleStatementContext))
		if err != nil {
			return nil, err
		}
		stmt.Init = init
	}

	if ctx.ELSE() != nil {
		if ctx.Block(1) != nil {
			elseBody, err := t.transformBlock(ctx.Block(1).(*grammar.BlockContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		} else if ctx.IfStatement() != nil {
			elseIf, err := t.transformIfStatement(ctx.IfStatement().(*grammar.IfStatementContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseIf
		}
	}

	return stmt, nil
}
