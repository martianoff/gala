package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"strings"
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

// ForbiddenStatementKeywords returns the set of Go-only statement keywords
// GALA rejects on its surface (GALA-E0036), keyed by name. It is the exported
// view of forbiddenStatementKeywordSuggestions so other passes — notably the
// analyzer's undefined-symbol check — can leave these names to the check that
// owns them instead of reporting the more generic "undefined", without
// re-declaring a list that could drift out of sync.
func ForbiddenStatementKeywords() map[string]bool {
	out := make(map[string]bool, len(forbiddenStatementKeywordSuggestions))
	for name := range forbiddenStatementKeywordSuggestions {
		out[name] = true
	}
	return out
}

// ForbiddenStatementKeywordSuggestion returns the GALA replacement GALA-E0036
// suggests for a Go-only statement keyword, so a check that meets the keyword
// in another shape (`go(f)`) can point at the same replacement.
func ForbiddenStatementKeywordSuggestion(name string) (string, bool) {
	s, ok := forbiddenStatementKeywordSuggestions[name]
	return s, ok
}

// checkForbiddenStatementKeyword rejects a bare Go-only statement keyword
// (`defer`, `go`, `goto`, `fallthrough`, `select`, `chan`) that the parser
// accepted as a lone identifier expression-statement. Such statements only
// "work" as an accident of the final gofmt round-trip (see
// forbiddenStatementKeywordSuggestions); GALA has native replacements, so this
// is a hard error (GALA-E0036).
//
// Unlike checkForbiddenGoBuiltinCall it needs no resolver guard: these are Go
// keywords, and the analyzer rejects a declaration spelled like one
// (GALA-E0055), so the name never refers to anything the program declared.
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
	return galaerr.NewCodedSemanticError(
		galaerr.CodeForbiddenStatementKeyword,
		exprCtx.GetStart().GetLine(), exprCtx.GetStart().GetColumn(),
		fmt.Sprintf("bare Go statement keyword %q is not part of GALA's surface", name),
		suggestion,
	)
}

func (t *galaASTTransformer) transformIncDecStmt(ctx *grammar.IncDecStmtContext) (ast.Stmt, error) {
	if name := t.immutableBindingName(ctx.Expression()); name != "" {
		return nil, t.semanticErrorAt(ctx, fmt.Sprintf("cannot increment/decrement immutable variable %s", name))
	}
	expr, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
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
		if retCtx.Expression() == nil {
			return &ast.ReturnStmt{}, nil
		}
		// A lambda, if-expression or match takes its types from the result
		// type of the innermost enclosing function or lambda.
		stmt, err := t.lowerReturnValue(retCtx.Expression())
		if err != nil {
			return nil, err
		}
		return stmt, nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformAssignment(ctx *grammar.AssignmentContext) (ast.Stmt, error) {
	lhsCtx := ctx.GetChild(0).(*grammar.ExpressionListContext)
	for _, exprCtx := range lhsCtx.AllExpression() {
		// `if (c) a() else x = v` parses as an assignment to the whole
		// if-expression: an if-expression's branch is an expression, and an
		// assignment is not one. Lowering it would assign to a function
		// call's result, which Go rejects; the intent is the braced
		// if-statement.
		if t.findIfExpressionInExpression(exprCtx) != nil {
			return nil, t.semanticErrorAt(ctx, "cannot assign to an if-expression: its else branch is an "+
				"expression, so `if (c) a() else x = v` assigns to the whole if. "+
				"Use braces to make the assignment part of a branch: `if (c) { a() } else { x = v }`")
		}
		// Only direct reassignment of a val (`v = ...`, `pkg.V = ...`) is blocked
		// here; a field or index through a val binding is checked below or by Go.
		if name := t.immutableBindingName(exprCtx); name != "" {
			return nil, t.semanticErrorAt(ctx, fmt.Sprintf("cannot assign to immutable variable %s", name))
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

	// A single bare variable's type is the RHS's expected type, so
	// `failure = Some(...)` emits `Some[string]{}.Apply(...)`.
	rhsListCtx := ctx.GetChild(2).(*grammar.ExpressionListContext)
	var lhsType transpiler.Type
	if lhsName, lhsOk := t.singleAssignmentLHSName(lhsCtx); lhsOk {
		lhsType = t.getValType(lhsName)
	}
	rhsExprs, err := t.transformExpressionListAgainst(rhsListCtx, lhsType)
	if err != nil {
		return nil, err
	}

	unwrappedRhs := make([]ast.Expr, len(rhsExprs))
	for i, r := range rhsExprs {
		unwrappedRhs[i] = t.unwrapImmutable(r)
	}
	// `a, err = goCall()` takes the call's results one by one.
	if len(unwrappedRhs) == 1 && len(lhsExprs) > 1 {
		unwrappedRhs[0] = t.rawGoCall(unwrappedRhs[0])
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

	// `a, b := f()` — several names from one multi-value expression. Lowered
	// like the `val a, b = f()` form; the per-name walk below handles only the
	// one-expression-per-name case.
	if len(rhsExprs) == 1 && len(idsCtx) > 1 {
		return t.shortVarDeclFromMultiValue(ctx, idsCtx, rhsExprs[0], mutable)
	}
	if len(rhsExprs) != len(idsCtx) {
		return nil, t.semanticErrorAt(ctx, "assignment mismatch")
	}

	lhs := make([]ast.Expr, 0)
	rhs := make([]ast.Expr, 0)
	for i, idCtx := range idsCtx {
		name := idCtx.GetText()
		typeName := t.getExprTypeName(rhsExprs[i])
		if qName := t.lookupTypeName(typeName.String()); !qName.IsNil() {
			typeName = qName
		}
		if mutable {
			t.addVar(name, typeName)
			t.markMutable(name)
		} else {
			t.addVal(name, typeName)
		}
		lhs = append(lhs, ast.NewIdent(name))

		val := t.unwrapImmutable(rhsExprs[i])
		t.bindGoResult(name, val)

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

// shortVarDeclFromMultiValue lowers `a, b := f()`, binding several names from
// one multi-value expression. The call is evaluated once into temporaries and
// each name takes its own, mirroring the `val a, b = f()` lowering in
// transformVarDeclaration.
func (t *galaASTTransformer) shortVarDeclFromMultiValue(
	ctx *grammar.ShortVarDeclContext,
	idsCtx []grammar.IIdentifierContext,
	rhs ast.Expr,
	mutable bool,
) (ast.Stmt, error) {
	callValue := t.rawGoCall(t.unwrapImmutable(rhs))
	// Only a call yields several values. Anything else — a literal, a name, an
	// arithmetic expression — is one value, and binding it to several names is
	// a mismatch Go would otherwise report against the generated temporaries.
	if _, isCall := callValue.(*ast.CallExpr); !isCall {
		return nil, t.semanticErrorAt(ctx, fmt.Sprintf(
			"assignment mismatch: %d names but the right-hand side is a single value", len(idsCtx)))
	}
	// Each name takes its type from the callee's corresponding declared return,
	// so `n, err := strconv.Atoi(s)` types `n` as int.
	callReturns := t.resolveGoCallReturnTypes(callValue)
	// The callee's return count is known only for a resolved Go signature; a
	// GALA callee reports none, and the mismatch surfaces later.
	if len(callReturns) > 0 && len(callReturns) != len(idsCtx) {
		return nil, t.semanticErrorAt(ctx, fmt.Sprintf(
			"assignment mismatch: %d names but the call returns %d values", len(idsCtx), len(callReturns)))
	}
	typeOf := func(i int) transpiler.Type {
		var typeName transpiler.Type = transpiler.NilType{}
		if i < len(callReturns) && callReturns[i] != nil {
			typeName = callReturns[i]
		}
		if typeName.IsNil() {
			return typeName
		}
		if qName := t.lookupTypeName(typeName.String()); !qName.IsNil() {
			typeName = qName
		}
		return typeName
	}

	// A mutable binding (the `for` init position) takes the results directly.
	// With no Immutable wrapper the statement stays a SimpleStmt, which is what
	// Go's for-clause accepts.
	if mutable {
		lhs := make([]ast.Expr, 0, len(idsCtx))
		for i, idCtx := range idsCtx {
			name := idCtx.GetText()
			t.addVar(name, typeOf(i))
			t.markMutable(name)
			lhs = append(lhs, ast.NewIdent(name))
		}
		return &ast.AssignStmt{Lhs: lhs, Tok: token.DEFINE, Rhs: []ast.Expr{callValue}}, nil
	}

	tempIdents := make([]*ast.Ident, len(idsCtx))
	for i := range idsCtx {
		tempIdents[i] = ast.NewIdent(t.nextTempVar())
	}
	specs := []ast.Spec{&ast.ValueSpec{Names: tempIdents, Values: []ast.Expr{callValue}}}
	for i, idCtx := range idsCtx {
		name := idCtx.GetText()
		t.addVal(name, typeOf(i))
		specs = append(specs, &ast.ValueSpec{
			Names: []*ast.Ident{ast.NewIdent(name)},
			Values: []ast.Expr{&ast.CallExpr{
				Fun:  t.stdIdent("NewImmutable"),
				Args: []ast.Expr{tempIdents[i]},
			}},
		})
	}
	return &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: specs}}, nil
}

// forClauseSlots splits a `for init; cond; post` clause into its two statement
// slots. Either may be omitted, so the statements are assigned by counting the
// `;` separators that precede each one rather than by their position in the
// list: nothing before the first separator is the init, anything after the
// second is the post.
func forClauseSlots(forClause *grammar.ForClauseContext) (init, post *grammar.SimpleStatementContext) {
	semis := 0
	for _, child := range forClause.GetChildren() {
		switch node := child.(type) {
		case antlr.TerminalNode:
			if node.GetText() == ";" {
				semis++
			}
		case *grammar.SimpleStatementContext:
			if semis == 0 {
				init = node
			} else {
				post = node
			}
		}
	}
	return init, post
}

// blockTail says what a block's trailing statement is for.
type blockTail int

const (
	// tailDiscarded: the trailing statement runs for its effect only, like
	// every other statement of the block — an if or for body, a void
	// function or lambda body. A bare value there is unused.
	tailDiscarded blockTail = iota
	// tailBranch: a branch of the trailing if-statement of a tailValue block.
	// Its trailing expression is lowered as a statement; the tailValue block
	// checks it once it knows whether the chain will be promoted (see
	// checkUnpromotedBranchTails).
	tailBranch
	// tailValue: the trailing expression is the block's value, and so is a
	// trailing if/else chain, whose branches the consumer promotes with
	// promoteIfBranchValues — a value-returning lambda body or a match arm.
	tailValue
	// tailExpr: the trailing expression is the block's value, but a trailing
	// if-statement is not promoted — a partial-function arm body.
	tailExpr
	// tailIIFE: only a trailing match or if-expression (lowered to an IIFE)
	// is known to produce the value; any other trailing statement is
	// discarded — a block lambda with no value expected of it.
	tailIIFE
	// tailReturn: the block is the body of a function declared with a result
	// type, or a branch of such a body's trailing if-statement. Its trailing
	// expression is the function's implicit return value, lowered exactly
	// like `return expr`.
	tailReturn
	// tailDropped: like tailDiscarded, for an arm of a statement-position
	// match or a branch of a statement-position if-expression, whose value
	// nothing reads. A trailing bare value there — which the expression form
	// allows — is evaluated and dropped rather than rejected as unused (see
	// dropValue).
	tailDropped
)

// transformBlock lowers a block whose trailing statement is discarded, like
// the trailing statement of an if or for body.
func (t *galaASTTransformer) transformBlock(ctx *grammar.BlockContext) (*ast.BlockStmt, error) {
	return t.transformBlockWithTail(ctx, tailDiscarded, slot{})
}

// transformValueBlock lowers a block whose trailing expression, or trailing
// if/else chain, is its value (a value-returning lambda body or a match arm). s is
// the slot that value fills, zero when unknown: a lambda, if or match tail is
// lowered against it (see lowerAgainst).
//
// A block whose value is discarded (s.discarded: an arm of a statement-position
// match) lowers its tail as a statement, so a trailing match there is a
// statement-position match too rather than a value mixing value and void arms.
func (t *galaASTTransformer) transformValueBlock(ctx *grammar.BlockContext, s slot) (*ast.BlockStmt, error) {
	if s.discarded {
		return t.transformBlockWithTail(ctx, tailDropped, slot{})
	}
	return t.transformBlockWithTail(ctx, tailValue, s)
}

// transformFunctionBody lowers the block body of a function declared with a
// result type: its trailing expression is the implicit return value.
func (t *galaASTTransformer) transformFunctionBody(ctx *grammar.BlockContext) (*ast.BlockStmt, error) {
	return t.transformBlockWithTail(ctx, tailReturn, slot{})
}

// transformBlockWithTail lowers a block. tail and lastValueExpected describe
// this block's trailing statement only; they are parameters, not transformer
// state, so nested and sibling blocks cannot inherit them.
func (t *galaASTTransformer) transformBlockWithTail(ctx *grammar.BlockContext, tail blockTail, lastValueExpected slot) (*ast.BlockStmt, error) {
	// A match or if-expression at the tail of every value-carrying block is
	// value-consumed, not statement-position — including a branch of a value
	// block's trailing if, whose tail the chain promotes like a lambda's.
	lastStmtIsValue := tail != tailDiscarded && tail != tailDropped
	t.pushScope()
	defer t.popScope()

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
			// The block's monad is the result type of the enclosing function
			// or lambda. When that is not known, a block whose value is
			// consumed takes it from its own trailing value, and fills the
			// enclosing lambda's slot with it.
			res := t.newBindResult(t.returnSlot.typ)
			if transpiler.IsUnusable(res.typ) && !lastStmtIsValue {
				return nil, t.semanticErrorAt(stmtCtx.(*grammar.StatementContext), "`bind` requires the enclosing function to declare a monad return type")
			}
			expr, err := t.desugarBindChain(allStmts[i:], res)
			if err != nil {
				return nil, err
			}
			if lastStmtIsValue {
				if res.guessed {
					t.returnSlot.guesses = append(t.returnSlot.guesses, expr)
				} else {
					t.tryFillReturnSlot(res.typ)
				}
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
		// expression is consumed (transformValueBlock) — function
		// bodies with a return type, lambda bodies, and match arm bodies
		// all use it, since their trailing expression becomes the
		// block's value.
		prev := t.matchInStatementPos
		isTrailing := i == lastIdx
		discardsValue := !isTrailing || !lastStmtIsValue
		if discardsValue && stmtIsBareMatchExpression(stmtCtx.(*grammar.StatementContext), t) {
			t.matchInStatementPos = true
		}
		var stmt ast.Stmt
		var err error
		valueExpr := trailingValueExpression(stmtCtx.(*grammar.StatementContext))
		// The trailing value of a lambda whose result type is not known yet
		// is one of its result values, like a `return` value.
		fillsPendingSlot := isTrailing && lastStmtIsValue && valueExpr != nil && ctx == t.returnSlot.body && t.returnSlotPending()
		ifCtx := ifStatementOf(stmtCtx.(*grammar.StatementContext))
		if ifExpr := t.findIfExpressionInExpression(valueExpr); discardsValue && ifExpr != nil {
			// An if-expression whose value nothing reads is an if statement.
			stmt, err = t.lowerIfExpressionStatement(ifExpr)
		} else if isTrailing && tail == tailReturn && valueExpr != nil {
			// The function's implicit return value.
			stmt, err = t.lowerFunctionTail(valueExpr)
		} else if isTrailing && ifCtx != nil && (tail == tailReturn || tail == tailValue || tail == tailBranch) {
			// A trailing if/else carries the block's value in its branches,
			// and so does an if/else nested at the tail of such a branch.
			branchTail := tailBranch
			if tail == tailReturn {
				branchTail = tailReturn
			}
			stmt, err = t.transformIfStatementWithTail(ifCtx, branchTail)
			if ifStmt, ok := stmt.(*ast.IfStmt); ok && err == nil && tail == tailValue {
				// The consumer promotes the chain only when every branch
				// ends in a value; otherwise its branch tails are discarded.
				if _, promotes := t.promoteIfBranchValues(ifStmt, plainReturn); !promotes {
					if err = t.checkMixedBranchValues(ifCtx, ifStmt); err == nil {
						err = t.checkUnpromotedBranchTails(ifCtx, ifStmt)
					}
				}
			}
		} else if isTrailing && lastStmtIsValue &&
			!transpiler.IsUnusable(lastValueExpected.typ) && t.needsExpectedType(valueExpr) {
			// The block's value fills a typed slot: a lambda, if or match tail is
			// lowered against it. A plain tail stays an ordinary statement.
			var expr ast.Expr
			if expr, err = t.lowerAgainst(valueExpr, lastValueExpected, true); err == nil {
				stmt = &ast.ExprStmt{X: expr}
			}
		} else if fillsPendingSlot {
			if err = t.checkForbiddenStatementKeyword(valueExpr); err == nil {
				stmt = &ast.ExprStmt{X: t.lowerFillingValue(valueExpr, false)}
			}
		} else {
			stmt, err = t.transformStatement(stmtCtx.(*grammar.StatementContext))
		}
		t.matchInStatementPos = prev
		if err != nil {
			return nil, err
		}
		// A statement-position match with a user-written `return X` inside an
		// arm body cannot be lowered as an IIFE (the bare return that
		// stripReturnStatements emits only exits the synthetic lambda, leaving
		// any enclosing for-loop spinning forever). buildMatchExpressionFromClauses
		// detects this case, builds the body as an inlined block, and stores
		// it in pendingMatchStmtBlock; we replace the placeholder ExprStmt
		// with the inlined block here so the user's `return X` becomes a real
		// Go return from the enclosing function.
		if t.pendingMatchStmtBlock != nil {
			block.List = append(block.List, t.pendingMatchStmtBlock)
			t.pendingMatchStmtBlock = nil
			continue
		}
		// A value is discarded by every statement but the trailing one of a
		// value-carrying block. At the tail of a tailIIFE block only a match
		// or if-expression carries the value — even when it fills a pending
		// slot, any other value is never promoted to the lambda's return.
		discarded := !isTrailing || tail == tailDiscarded ||
			(tail == tailIIFE && !t.needsExpectedType(valueExpr))
		if isTrailing && tail == tailDropped && valueExpr != nil {
			stmt = t.dropValue(valueExpr, stmt)
		} else if discarded && valueExpr != nil {
			hint := functionDiscardHint
			if isTrailing && tail == tailIIFE {
				hint = lambdaDiscardHint
			}
			if err := t.checkValueUsedHint(valueExpr, stmt, hint); err != nil {
				return nil, err
			}
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
	exprCtx := trailingValueExpression(ctx)
	return exprCtx != nil && t.expressionIsBareMatch(exprCtx)
}

// trailingValueExpression returns the expression of an expression statement
// (statement → declaration → simpleStatement → expression), or nil when the
// statement is anything else.
func trailingValueExpression(ctx *grammar.StatementContext) grammar.IExpressionContext {
	if ctx == nil {
		return nil
	}
	dc, ok := ctx.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	sc, ok := dc.SimpleStatement().(*grammar.SimpleStatementContext)
	if !ok || sc == nil {
		return nil
	}
	return sc.Expression()
}

// ifStatementOf returns the if-statement ctx consists of, or nil.
func ifStatementOf(ctx *grammar.StatementContext) *grammar.IfStatementContext {
	if ctx == nil {
		return nil
	}
	dc, ok := ctx.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	ifCtx, _ := dc.IfStatement().(*grammar.IfStatementContext)
	return ifCtx
}

// lowerIfExpressionStatement lowers an if-expression whose value is discarded —
// `if (ok) fmt.Println("a") else fmt.Println("b")` on its own line — as a Go if
// statement whose branches are statements, as an if statement's are: a Go call
// there is made as it is rather than converted to a value, no branch has to
// produce a value, and a `return` in a block branch returns from the enclosing
// function.
func (t *galaASTTransformer) lowerIfExpressionStatement(ctx *grammar.IfExpressionContext) (ast.Stmt, error) {
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	ifStmt := &ast.IfStmt{Cond: cond}
	for i, b := range ctx.AllIfExprBranch() {
		branch := b.(*grammar.IfExprBranchContext)
		var stmt ast.Stmt
		if blockCtx, ok := branch.Block().(*grammar.BlockContext); ok {
			stmt, err = t.transformBlockWithTail(blockCtx, tailDropped, slot{})
		} else {
			stmt, err = t.lowerExpressionStatement(branch.Expression())
		}
		if err != nil {
			return nil, err
		}
		body, isBlock := stmt.(*ast.BlockStmt)
		switch {
		case i == 0 && isBlock:
			ifStmt.Body = body
		case i == 0:
			ifStmt.Body = &ast.BlockStmt{List: []ast.Stmt{stmt}}
		case isBlock:
			ifStmt.Else = body
		default:
			// An else branch that is itself an if stays an `else if`.
			ifStmt.Else = stmt
			if _, isIf := stmt.(*ast.IfStmt); !isIf {
				ifStmt.Else = &ast.BlockStmt{List: []ast.Stmt{stmt}}
			}
		}
	}
	return ifStmt, nil
}

// lowerExpressionStatement lowers an expression whose value is discarded the
// way a block lowers such a statement: a bare match is a statement-position
// match, a bare if-expression an if statement, and a plain value is dropped
// (see dropValue).
func (t *galaASTTransformer) lowerExpressionStatement(exprCtx grammar.IExpressionContext) (ast.Stmt, error) {
	if ifExpr := t.findIfExpressionInExpression(exprCtx); ifExpr != nil {
		return t.lowerIfExpressionStatement(ifExpr)
	}
	if err := t.checkForbiddenStatementKeyword(exprCtx); err != nil {
		return nil, err
	}
	prev := t.matchInStatementPos
	t.matchInStatementPos = t.expressionIsBareMatch(exprCtx)
	expr, err := t.transformExpression(exprCtx)
	t.matchInStatementPos = prev
	if err != nil {
		return nil, err
	}
	if block := t.pendingMatchStmtBlock; block != nil {
		t.pendingMatchStmtBlock = nil
		return block, nil
	}
	return t.dropValue(exprCtx, &ast.ExprStmt{X: expr}), nil
}

// dropValue returns stmt, the statement lowered from exprCtx, with a bare value
// that Go would reject as unused (`true`, `label`) assigned to `_` instead:
// still evaluated, and dropped. A call is left as it is.
func (t *galaASTTransformer) dropValue(exprCtx grammar.IExpressionContext, stmt ast.Stmt) ast.Stmt {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || t.checkValueUsedHint(exprCtx, stmt, "") == nil {
		return stmt
	}
	return &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []ast.Expr{exprStmt.X}}
}

// lowerFunctionTail lowers the trailing expression of a function declared with
// a result type. It is the function's implicit return value, lowered exactly
// like `return expr`, so it takes its types from the result type the same way.
// A diverging call (`panic`/`Panic`) is left a statement, since it needs no
// value; an expression that produces no value is rejected, since the function
// would then finish without one.
func (t *galaASTTransformer) lowerFunctionTail(exprCtx grammar.IExpressionContext) (ast.Stmt, error) {
	if err := t.checkForbiddenStatementKeyword(exprCtx); err != nil {
		return nil, err
	}
	ret, err := t.lowerReturnValue(exprCtx)
	if err != nil {
		return nil, err
	}
	value := ret.Results[0]
	if t.isNoReturnCallExpr(value) {
		return &ast.ExprStmt{X: t.lowerPanicWrapperToBuiltin(value)}, nil
	}
	if _, void := t.getExprTypeName(value).(transpiler.VoidType); void || isVoidIIFE(value) {
		return nil, t.semanticErrorAt(exprCtx, fmt.Sprintf(
			"function %s returns %s, but ends in `%s`, which produces no value; end the body in the value to return, or add a `return`",
			t.returnSlot.funcName, t.returnSlot.typ, exprCtx.GetText()))
	}
	return ret, nil
}

// sourceFunctionName is the name a diagnostic gives a function declaration:
// `Name`, or `Type.Name` for a method. It is the GALA spelling, never the
// standalone `Type_Name` a generic method is lowered to. receiverTypeName is
// the receiver's resolved base type name, "" for a plain function.
func (t *galaASTTransformer) sourceFunctionName(ctx *grammar.FunctionDeclarationContext, receiverTypeName string) string {
	name := ctx.Identifier().GetText()
	if receiverTypeName == "" {
		return name
	}
	return strings.TrimPrefix(receiverTypeName, t.packageName+".") + "." + name
}

// missingReturnError reports a function body that can finish without the value
// its result type promises, at the body's last statement (or its closing brace
// when it is empty).
func (t *galaASTTransformer) missingReturnError(body *grammar.BlockContext, name string, result transpiler.Type) error {
	msg := fmt.Sprintf("function %s returns %s, but its body can finish without a value; end it in the value to return, or add a `return`", name, result)
	if stmts := body.AllStatement(); len(stmts) > 0 {
		return t.semanticErrorAt(stmts[len(stmts)-1], msg)
	}
	if stop := body.GetStop(); stop != nil {
		return galaerr.NewSemanticErrorInFile(t.filePath, stop.GetLine(), stop.GetColumn(), msg)
	}
	return t.semanticErrorAt(body, msg)
}

// isTerminatingStmt reports whether s is a terminating statement in the sense
// of the Go specification, so a function body ending in it cannot fall off its
// end. It errs toward true: a loop without a condition, a switch, a select
// and a labeled statement count as terminating without checking them for a
// `break`, so it never rejects a body Go would accept.
func isTerminatingStmt(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.GOTO || s.Tok == token.FALLTHROUGH
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Fun.(*ast.Ident)
		return ok && ident.Name == "panic"
	case *ast.BlockStmt:
		for i := len(s.List) - 1; i >= 0; i-- {
			if _, empty := s.List[i].(*ast.EmptyStmt); !empty {
				return isTerminatingStmt(s.List[i])
			}
		}
		return false
	case *ast.IfStmt:
		return s.Else != nil && isTerminatingStmt(s.Body) && isTerminatingStmt(s.Else)
	case *ast.ForStmt:
		return s.Cond == nil
	case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.LabeledStmt:
		return true
	}
	return false
}

// checkMixedBranchValues rejects a complete if/else chain, lowered with
// tailBranch at the tail of a value block, where one branch ends in a value
// and another produces none (a void call, an assignment, an empty block). The
// chain is then not the block's value, and neither is the value the other
// branch computes. A branch ending in a `return` or a diverging call needs no
// value; an `if` with no `else`, here or nested at a branch's tail, is left to
// checkUnpromotedBranchTails.
func (t *galaASTTransformer) checkMixedBranchValues(ifCtx *grammar.IfStatementContext, stmt *ast.IfStmt) error {
	if stmt.Else == nil {
		return nil
	}
	valueSeen := false
	var noValue antlr.ParserRuleContext
	var walk func(*grammar.IfStatementContext, *ast.IfStmt)
	branch := func(blockCtx grammar.IBlockContext, blk *ast.BlockStmt) {
		stmts := blockCtx.(*grammar.BlockContext).AllStatement()
		if len(stmts) == 0 || blk == nil || len(blk.List) == 0 {
			if noValue == nil {
				noValue = blockCtx.(*grammar.BlockContext)
			}
			return
		}
		last := stmts[len(stmts)-1].(*grammar.StatementContext)
		yields := false
		switch lowered := blk.List[len(blk.List)-1].(type) {
		case *ast.IfStmt:
			if innerCtx := ifStatementOf(last); innerCtx != nil {
				if lowered.Else != nil {
					walk(innerCtx, lowered)
				}
				return
			}
		case *ast.ReturnStmt:
			return
		case *ast.ExprStmt:
			if t.isNoReturnCallExpr(lowered.X) {
				return
			}
			_, void := t.getExprTypeName(lowered.X).(transpiler.VoidType)
			yields = !void && !isVoidIIFE(lowered.X)
		}
		if yields {
			valueSeen = true
		} else if noValue == nil {
			noValue = last
		}
	}
	walk = func(ifCtx *grammar.IfStatementContext, stmt *ast.IfStmt) {
		branch(ifCtx.Block(0), stmt.Body)
		switch e := stmt.Else.(type) {
		case *ast.BlockStmt:
			if ifCtx.Block(1) != nil {
				branch(ifCtx.Block(1), e)
			}
		case *ast.IfStmt:
			if elseIf, ok := ifCtx.IfStatement().(*grammar.IfStatementContext); ok && elseIf != nil {
				walk(elseIf, e)
			}
		}
	}
	walk(ifCtx, stmt)
	if valueSeen && noValue != nil {
		return t.semanticErrorAt(noValue,
			"this branch of the `if` produces no value, but another branch does; end every branch in a value, since the `if` is the block's value")
	}
	return nil
}

// checkUnpromotedBranchTails runs checkValueUsed on the trailing statement of
// every branch of an if/else chain lowered with tailBranch, once it is known
// the chain will not be promoted: its branch tails are then discarded like
// any other statement. ifCtx is the GALA chain and stmt its lowering.
func (t *galaASTTransformer) checkUnpromotedBranchTails(ifCtx *grammar.IfStatementContext, stmt *ast.IfStmt) error {
	checkBlock := func(blockCtx grammar.IBlockContext, blk *ast.BlockStmt) error {
		stmts := blockCtx.(*grammar.BlockContext).AllStatement()
		if len(stmts) == 0 || blk == nil || len(blk.List) == 0 {
			return nil
		}
		last := stmts[len(stmts)-1].(*grammar.StatementContext)
		lowered := blk.List[len(blk.List)-1]
		if inner, ok := lowered.(*ast.IfStmt); ok {
			if innerCtx := ifStatementOf(last); innerCtx != nil {
				return t.checkUnpromotedBranchTails(innerCtx, inner)
			}
		}
		if valueExpr := trailingValueExpression(last); valueExpr != nil {
			return t.checkValueUsed(valueExpr, lowered)
		}
		return nil
	}
	if err := checkBlock(ifCtx.Block(0), stmt.Body); err != nil {
		return err
	}
	switch e := stmt.Else.(type) {
	case *ast.BlockStmt:
		if ifCtx.Block(1) != nil {
			return checkBlock(ifCtx.Block(1), e)
		}
	case *ast.IfStmt:
		if elseIf, ok := ifCtx.IfStatement().(*grammar.IfStatementContext); ok && elseIf != nil {
			return t.checkUnpromotedBranchTails(elseIf, e)
		}
	}
	return nil
}

// checkValueUsed rejects an expression statement whose value is discarded
// without any effect: a bare name, literal, field access or operator
// expression. Go accepts only calls and receives as expression statements; a
// bare `val` name also lowers to a call (its `.Get()`), which Go would accept
// silently, so the GALA source decides. stmt is exprCtx's lowering.
func (t *galaASTTransformer) checkValueUsed(exprCtx grammar.IExpressionContext, stmt ast.Stmt) error {
	return t.checkValueUsedHint(exprCtx, stmt, functionDiscardHint)
}

// Hints for checkValueUsedHint's diagnostic: how to keep a discarded value.
const (
	functionDiscardHint = "remove it, or return it from a function that declares a result type"
	// lambdaDiscardHint is for the trailing value of a block lambda with no
	// value expected of it, which a declared result type or a `return` makes
	// the lambda's result.
	lambdaDiscardHint = "remove it, or make it the lambda's result: declare a result type, as in `(x int) int => { ... }`, or write `return` before it"
)

// checkValueUsedHint is checkValueUsed with the diagnostic's hint given.
func (t *galaASTTransformer) checkValueUsedHint(exprCtx grammar.IExpressionContext, stmt ast.Stmt, hint string) error {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return nil
	}
	x := ast.Unparen(exprStmt.X)
	switch e := x.(type) {
	case *ast.CallExpr:
		if !t.isDirectVariableExpression(exprCtx) {
			return nil
		}
		// A bare name that lowers to a call is a val read through `.Get()`.
		if sel, ok := e.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Get" || len(e.Args) != 0 {
			return nil
		}
	case *ast.UnaryExpr:
		if e.Op == token.ARROW {
			return nil
		}
	case *ast.Ident:
		// `break` and `continue` parse as bare names and print as the Go
		// statements.
		if e.Name == "break" || e.Name == "continue" {
			return nil
		}
	}
	return t.semanticErrorAt(exprCtx, fmt.Sprintf(
		"`%s` is evaluated but not used; %s", exprCtx.GetText(), hint))
}

func (t *galaASTTransformer) transformForStatement(ctx *grammar.ForStatementContext) (ast.Stmt, error) {
	// Handle condition-only for loop: for condition { ... }
	if condCtx := ctx.ForCondition(); condCtx != nil {
		cond, err := t.transformExpression(condCtx.(*grammar.ForConditionContext).Expression())
		if err != nil {
			return nil, err
		}
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

		// Both slots of `for init; cond; post` are optional, so the statement
		// list is positionally ambiguous: with the init omitted, the post is
		// the only entry and reading it as element 0 turned `for ; i < 3; i =
		// i + 1` into `for i = i + 1; i < 3; {`, running the step once before
		// the loop instead of on every iteration. The slot is decided by how
		// many `;` separators precede each statement.
		initCtx, postCtx := forClauseSlots(forClause)

		// Process init FIRST so variables are in scope for condition and body
		// Note: init uses transformForLoopInitStatement to make := declarations mutable
		if initCtx != nil {
			init, err = t.transformForLoopInitStatement(initCtx)
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
		}

		// Process post (can use init variables)
		if postCtx != nil {
			post, err = t.transformSimpleStatement(postCtx)
			if err != nil {
				return nil, err
			}
			// A multi-value `a, b := f()` lowers to a `var (...)` block, which
			// Go accepts in neither the post nor any other SimpleStmt slot. It
			// reads as a loop step but binds names nothing can use, so it is
			// reported here rather than reaching the Go parser as an
			// unparseable-output internal error.
			if _, isDecl := post.(*ast.DeclStmt); isDecl {
				return nil, t.semanticErrorAt(postCtx, "a `for` post statement cannot bind a multi-value call; move the binding into the loop body")
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
	postfix := LeadingPostfixExpr(ctx, true)
	return postfix != nil && len(postfix.AllPostfixSuffix()) == 0
}

// immutableBindingName returns the source spelling of the val that ctx names
// directly — `Name`, or `pkg.Name` for an imported package-level val — or ""
// for anything else (a var, a field or index through a val, a call).
func (t *galaASTTransformer) immutableBindingName(ctx grammar.IExpressionContext) string {
	postfix := LeadingPostfixExpr(ctx, true)
	primary := PrimaryOf(postfix)
	if primary == nil || primary.Identifier() == nil {
		return ""
	}
	pkg, name := "", primary.Identifier().GetText()
	switch suffixes := postfix.AllPostfixSuffix(); len(suffixes) {
	case 0:
	case 1:
		sel := suffixes[0].(*grammar.PostfixSuffixContext).Identifier()
		if sel == nil {
			return ""
		}
		pkg, name = name, sel.GetText()
	default:
		return ""
	}
	if b, ok := t.lookupBinding(pkg, name); ok && b.isVal {
		return b.String()
	}
	return ""
}

func (t *galaASTTransformer) transformIfStatement(ctx *grammar.IfStatementContext) (ast.Stmt, error) {
	return t.transformIfStatementWithTail(ctx, tailDiscarded)
}

// transformIfStatementWithTail lowers an if-statement whose branch blocks end
// as tail says: tailDiscarded for an if run for its effect, or the branch tail
// of a value block's trailing if/else.
func (t *galaASTTransformer) transformIfStatementWithTail(ctx *grammar.IfStatementContext, tail blockTail) (ast.Stmt, error) {
	if err := checkIfInitializer(ctx); err != nil {
		return nil, err
	}

	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	body, err := t.transformBlockWithTail(ctx.Block(0).(*grammar.BlockContext), tail, slot{})
	if err != nil {
		return nil, err
	}
	stmt := &ast.IfStmt{
		Cond: cond,
		Body: body,
	}

	if ctx.ELSE() != nil {
		if ctx.Block(1) != nil {
			elseBody, err := t.transformBlockWithTail(ctx.Block(1).(*grammar.BlockContext), tail, slot{})
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		} else if ctx.IfStatement() != nil {
			elseIf, err := t.transformIfStatementWithTail(ctx.IfStatement().(*grammar.IfStatementContext), tail)
			if err != nil {
				return nil, err
			}
			stmt.Else = elseIf
		}
	}

	return stmt, nil
}

// checkIfInitializer rejects Go's `if init; cond { }` with GALA-E0047.
//
// The initializer slot exists in the grammar (`ifStatement: 'if'
// (simpleStatement ';')? expression block`) but is not part of GALA's
// statement surface, alongside the Go-only keywords in
// forbiddenStatementKeywordSuggestions. Only the initializer is rejected; a
// plain condition, `else`, `else if` and the if-expression are unaffected.
//
// The hint points at `Try`, which auto-wraps the Go `(T, error)` return this
// slot is typically used to nil-test into Success/Failure.
func checkIfInitializer(ctx *grammar.IfStatementContext) error {
	if ctx.SimpleStatement() == nil {
		return nil
	}
	line, col := ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
	return galaerr.NewCodedSemanticError(
		galaerr.CodeIfInitializer,
		line, col,
		"`if` takes no initializer statement",
		"wrap the call in `Try(...)` and `match` on `Success(v)` / `Failure(e)` — a Go `(T, error)` return is auto-wrapped",
	).WithSpan(col + len("if"))
}
