package transformer

import (
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Control flow out of a match or if-expression whose value is used.
//
// Such a construct lowers to a function literal called at once, so a `return`
// in one of its arms would leave only that function — the match — and a
// `break` / `continue` would name no loop. When the construct is the whole
// initializer of a local `val` or `var`, or the whole value assigned to a
// variable, it is lowered as statements instead: a variable of the construct's
// type is declared before the statement, each arm stores its value in it, and
// the statement reads it. A `return`, `break` or `continue` in an arm then
// acts on the enclosing function or loop, as it does in a statement-position
// match.
//
// A construct is lowered this way only when it needs to be: when its arms hold
// such a statement outside any lambda (escapesConstruct). An arm whose value is
// itself such a construct stores into the same variable. Inside a larger
// expression it cannot be: the rest of the expression would have to be
// evaluated around it. There a `return` that would leave only the construct is
// GALA-E0069 and a `break` / `continue` GALA-E0059 (see branching_calls.go and
// loop_control.go).
//
// slot.hoist names the variable a construct lowered this way stores its value
// in ("" when it is lowered to a function literal).

// hoistedValue is a construct lowered as statements: stmts store its value, of
// type typ, in its variable.
type hoistedValue struct {
	stmts []ast.Stmt
	typ   transpiler.Type
}

// escapesConstruct reports whether any of trees holds a `return`, `break` or
// `continue` outside any lambda: one that, in a construct lowered to a
// function literal, could not reach the function or loop it is written for. A
// `break` / `continue` inside a `for` loop of the tree's own controls that
// loop. The answer for each tree is cached: a construct and those nested in
// it are asked about at every level of the lowering.
func escapesConstruct[T antlr.Tree](t *galaASTTransformer, trees ...T) bool {
	for _, tree := range trees {
		key := antlr.Tree(tree)
		escaping, ok := t.escapeCache[key]
		if !ok {
			escaping = escapes(key, false)
			if t.escapeCache == nil {
				t.escapeCache = make(map[antlr.Tree]bool)
			}
			t.escapeCache[key] = escaping
		}
		if escaping {
			return true
		}
	}
	return false
}

func escapes(tree antlr.Tree, inLoop bool) bool {
	switch n := tree.(type) {
	case *grammar.LambdaExpressionContext:
		return false
	case *grammar.ReturnStatementContext:
		return true
	case *grammar.ForStatementContext:
		inLoop = true
	case antlr.TerminalNode:
		_, ok := loopControlToken(n.GetText())
		return ok && !inLoop
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		if escapes(tree.GetChild(i), inLoop) {
			return true
		}
	}
	return false
}

// hoistable reports whether exprCtx is a match or if-expression (possibly
// parenthesized) whose arms escapesConstruct.
func (t *galaASTTransformer) hoistable(exprCtx grammar.IExpressionContext) bool {
	f := t.classifyExpr(exprCtx)
	switch {
	case f.grouped != nil:
		return t.hoistable(f.grouped)
	case f.ifExpr != nil:
		return escapesConstruct(t, f.ifExpr.AllIfExprBranch()...)
	case f.match != nil:
		return escapesConstruct(t, f.match.AllCaseClause()...)
	}
	return false
}

// lowerDeclarationInitializers lowers the values of a `val` or `var`
// declaring names names, or of an assignment to names targets, with declared
// type declared (nil when none). The single value of a single local name is
// lowered as statements when hoistable: the statement then reads the variable
// they store its value in, and they are left in t.hoistedPre for
// transformStatement to emit before it.
func (t *galaASTTransformer) lowerDeclarationInitializers(ctx *grammar.ExpressionListContext, names int, declared transpiler.Type) ([]ast.Expr, error) {
	local := t.localDeclaration
	t.localDeclaration = false
	exprs := ctx.AllExpression()
	if !local || names != 1 || len(exprs) != 1 || !t.hoistable(exprs[0]) {
		return t.transformExpressionListAgainst(ctx, declared)
	}
	target := t.nextTempVar()
	expr, err := t.lowerAgainst(exprs[0], slot{typ: declared, hoist: target}, true)
	if err != nil {
		return nil, err
	}
	hv, ok := t.takeHoisted(expr)
	if !ok {
		return []ast.Expr{expr}, nil
	}
	t.addVar(target, hv.typ)
	t.hoistedPre = append([]ast.Stmt{seqVarDecl(target, t.typeToExpr(hv.typ))}, hv.stmts...)
	return []ast.Expr{ast.NewIdent(target)}, nil
}

// hoistedResult records stmts, storing a value of type typ in target, as the
// lowering of the construct at line:col and returns the placeholder
// expression that stands for it until its consumer takes it (takeHoisted).
// The placeholder has the type of the value, for the arm that holds the
// construct. What stmts acquire is released at their end (scopeReleases).
func (t *galaASTTransformer) hoistedResult(target string, stmts []ast.Stmt, typ transpiler.Type, line, col int) (ast.Expr, error) {
	stmts, err := t.scopeReleases(stmts, line, col)
	if err != nil {
		return nil, err
	}
	ph := ast.NewIdent(target)
	if t.hoisted == nil {
		t.hoisted = make(map[*ast.Ident]hoistedValue)
	}
	t.hoisted[ph] = hoistedValue{stmts: stmts, typ: typ}
	t.exprTypeCache[ph] = typ
	return ph, nil
}

// takeHoisted returns the construct expr stands for, if it is a placeholder
// hoistedResult returned, and forgets it.
func (t *galaASTTransformer) takeHoisted(expr ast.Expr) (hoistedValue, bool) {
	ident, ok := ast.Unparen(expr).(*ast.Ident)
	if !ok {
		return hoistedValue{}, false
	}
	hv, ok := t.hoisted[ident]
	if ok {
		delete(t.hoisted, ident)
	}
	return hv, ok
}

// storeArmValues rewrites, in the lowered arms stmts of a hoisted match, each
// synthesized arm-tail `return X` into the store of X in target, of type typ
// (storeValue). A user `return` is left as it is: it leaves the function.
func (t *galaASTTransformer) storeArmValues(stmts []ast.Stmt, target string, typ transpiler.Type) []ast.Stmt {
	return t.rewriteSynthesizedArmReturns(stmts, func(value ast.Expr) []ast.Stmt {
		return t.storeValue(value, target, typ)
	})
}

// storeValue is the store of value in target, of type typ: the statements of
// a hoisted construct, which store their own value, or `target = value`. A
// name of type `any` is asserted to typ, as a match lowered to a function
// literal returns it (see fixupReturnStatement).
func (t *galaASTTransformer) storeValue(value ast.Expr, target string, typ transpiler.Type) []ast.Stmt {
	if hv, ok := t.takeHoisted(value); ok {
		return []ast.Stmt{&ast.BlockStmt{List: hv.stmts}}
	}
	if !transpiler.IsUnusableOrAny(typ) {
		value = t.assertAnyIdent(value, typ)
	}
	return []ast.Stmt{assignStmt(target, value)}
}

// hoistedType is the type of the variable a construct of kind lowered as
// statements stores its value in, from typ, the type its storing branches
// unify to against s. When every branch leaves (allLeave) the declaration is
// never reached, and the variable takes the type the values of the source
// `return`s in branches share — the type the construct had when it was lowered
// to a function literal those `return`s left. A construct with no such type
// is GALA-E0068.
func (t *galaASTTransformer) hoistedType(kind string, typ transpiler.Type, s slot, allLeave bool, line, col int, branches ...[]ast.Stmt) (transpiler.Type, error) {
	if allLeave {
		if leaving := t.leavingValuesType(branches...); leaving != nil {
			return leaving, nil
		}
		return nil, leavingBranchingError(kind, line, col)
	}
	typ = t.branchingResultType(typ, s)
	if transpiler.IsUnusable(typ) || typ.IsVoid() {
		return nil, untypedBranchingError(kind, line, col)
	}
	return typ, nil
}

// armBranchResult is the value the promoted trailing if/else of a match arm
// gives the arm (see firstBranchResult). In a match lowered as statements
// (s.hoist) a user `return` in a branch leaves the function rather than giving
// the arm a value, so only a synthesized arm-tail return counts.
func (t *galaASTTransformer) armBranchResult(promoted *ast.IfStmt, s slot) ast.Expr {
	if s.hoist == "" {
		return firstBranchResult(promoted)
	}
	var result ast.Expr
	ast.Inspect(promoted, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if result == nil && len(x.Results) == 1 && t.isSynthesizedArmReturn(x) {
				result = x.Results[0]
			}
		}
		return result == nil
	})
	return result
}

// leaves reports whether stmts, the lowered body of a branch, leave on every
// path: they end in a source `return`, a `break` or `continue`, a `panic`, or
// an if/else every branch of which leaves.
func (t *galaASTTransformer) leaves(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	switch last := stmts[len(stmts)-1].(type) {
	case *ast.ReturnStmt:
		_, user := t.userReturns[last]
		return user
	case *ast.BranchStmt:
		return true
	case *ast.ExprStmt:
		// A diverging call (`panic(...)`, which `Panic` lowers to).
		return isTerminatingStmt(last)
	case *ast.BlockStmt:
		return t.leaves(last.List)
	case *ast.IfStmt:
		if last.Body == nil || last.Else == nil || !t.leaves(last.Body.List) {
			return false
		}
		return t.leaves([]ast.Stmt{last.Else})
	}
	return false
}

// armLeaves reports whether a lowered match arm leaves on every path: clause
// is a regular arm's `if cond { body }`, possibly after its bindings, or nil
// for the default arm, whose body is defaultBody.
func (t *galaASTTransformer) armLeaves(clause ast.Stmt, defaultBody []ast.Stmt) bool {
	if clause == nil {
		return t.leaves(defaultBody)
	}
	if block, ok := clause.(*ast.BlockStmt); ok && len(block.List) > 0 {
		clause = block.List[len(block.List)-1]
	}
	ifStmt, ok := clause.(*ast.IfStmt)
	return ok && ifStmt.Body != nil && t.leaves(ifStmt.Body.List)
}

// leavingValuesType is the settled type the values of the source `return`s in
// branches (outside function literals) share, or nil.
func (t *galaASTTransformer) leavingValuesType(branches ...[]ast.Stmt) transpiler.Type {
	var types []transpiler.Type
	visit := func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if _, user := t.userReturns[x]; user && len(x.Results) == 1 {
				types = append(types, t.inferResultType(x.Results[0]))
			}
		}
		return true
	}
	for _, stmts := range branches {
		for _, s := range stmts {
			ast.Inspect(s, visit)
		}
	}
	return t.siblingsType(types)
}

// Resources a construct lowered as statements acquires.
//
// A `use` lowers to a Go `defer`, which runs when the Go function it is in
// returns. A construct lowered to a function literal releases what its arms
// acquire at its own end; lowered as statements, its arms are part of the
// enclosing function, and a `use` in one would hold its resource until that
// function returns — in a loop, every iteration's. So a construct whose
// statements hold a `defer` (outside any function literal) runs them in a
// function literal of their own, called at once, and carries the control
// flow that leaves them out of it:
//
//	var _exit int // how the statements left; 0 at their end
//	var _ret R    // the value a `return` in them returns
//	func() {
//		... _ret = x; _exit = 1; return // was `return x`
//		... _exit = 2; return           // was `break`
//	}()
//	if _exit == 1 { return _ret }
//	if _exit == 2 { break }
//
// The construct's value is stored in its variable, declared before them, as
// before. A construct nested in such statements has already run its own
// statements this way.

// The ways statements run in a function literal of their own (see
// scopeReleases) leave it.
const (
	exitReturn = iota + 1
	exitBreak
	exitContinue
)

// releaseScope rewrites the statements scopeReleases runs in a function
// literal: each statement that leaves them — a `return`, or a `break` /
// `continue` lowered from source that reaches a loop around them — records
// how in exit and returns from the literal. A `return` with a value stores it
// in result first (hasValue); leaving[k] is the first statement that left by
// k, whose source position its dispatch keeps.
type releaseScope struct {
	t        *galaASTTransformer
	exit     string
	result   string
	hasValue bool
	leaving  [exitContinue + 1]ast.Stmt
}

// scopeReleases runs stmts, the statements of a construct lowered as
// statements, in a function literal of their own when they hold a `defer` —
// a `use` — so what they acquire is released at their end. A construct at
// line:col whose `return` has a value of no known type to hold is an error.
func (t *galaASTTransformer) scopeReleases(stmts []ast.Stmt, line, col int) ([]ast.Stmt, error) {
	if !anyOutsideFuncLits(stmts, func(n ast.Node) bool { _, ok := n.(*ast.DeferStmt); return ok }) {
		return stmts, nil
	}
	// The type a `return` value is held in: the enclosing function's, or,
	// while a lambda's is not known yet, the one its values share.
	retType := t.returnSlot.typ
	if transpiler.IsUnusable(retType) {
		retType = t.leavingValuesType(stmts)
	}
	r := &releaseScope{t: t, exit: t.nextTempVar(), result: t.nextTempVar()}
	call := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: r.rewriteList(stmts, false)},
	}}}
	var dispatch []ast.Stmt
	for k := exitReturn; k <= exitContinue; k++ {
		if left := r.leaving[k]; left != nil {
			dispatch = append(dispatch, &ast.IfStmt{
				Cond: &ast.BinaryExpr{X: ast.NewIdent(r.exit), Op: token.EQL, Y: intLit(k)},
				Body: &ast.BlockStmt{List: []ast.Stmt{r.dispatch(k, left)}},
			})
		}
	}
	if len(dispatch) == 0 {
		return []ast.Stmt{call}, nil
	}
	out := []ast.Stmt{seqVarDecl(r.exit, ast.NewIdent("int"))}
	if r.hasValue {
		// Only a lambda's result type can still be unknown here.
		if transpiler.IsUnusable(retType) {
			return nil, galaerr.NewSemanticErrorAt(line, col,
				"cannot infer the result type of this lambda, which holds the value a `return` beside a `use` in this construct returns — annotate the lambda's result type (e.g. `(x int) Option[int] => { ... }`)")
		}
		t.addVar(r.result, retType)
		out = append(out, seqVarDecl(r.result, t.typeToExpr(retType)))
	}
	out = append(append(out, call), dispatch...)
	return []ast.Stmt{&ast.BlockStmt{List: out}}, nil
}

// dispatch is the statement that leaves the way k names once the function
// literal has returned, standing for left, the first statement in it that
// left so. It keeps left's source position, to be checked as left would be
// (see checkBranchingCalls and checkLoopControl).
func (r *releaseScope) dispatch(k int, left ast.Stmt) ast.Stmt {
	t := r.t
	if k == exitReturn {
		ret := &ast.ReturnStmt{}
		if r.hasValue {
			ret.Results = []ast.Expr{ast.NewIdent(r.result)}
		}
		if site, ok := t.userReturns[left.(*ast.ReturnStmt)]; ok {
			t.recordUserReturn(ret, site.line, site.col)
		}
		return ret
	}
	bs := &ast.BranchStmt{Tok: token.BREAK}
	if k == exitContinue {
		bs.Tok = token.CONTINUE
	}
	t.loopControlSites[bs] = t.loopControlSites[left.(*ast.BranchStmt)]
	return bs
}

// rewriteList rewrites stmts in place (see releaseScope); inLoop reports
// whether a loop written in source among the statements encloses them.
func (r *releaseScope) rewriteList(stmts []ast.Stmt, inLoop bool) []ast.Stmt {
	for i, s := range stmts {
		stmts[i] = r.rewrite(s, inLoop)
	}
	return stmts
}

func (r *releaseScope) rewrite(stmt ast.Stmt, inLoop bool) ast.Stmt {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		var store []ast.Stmt
		if len(s.Results) == 1 {
			r.hasValue = true
			store = []ast.Stmt{assignStmt(r.result, s.Results[0])}
		}
		return r.leave(exitReturn, s, store...)
	case *ast.BranchStmt:
		if _, user := r.t.loopControlSites[s]; user && !inLoop {
			k := exitBreak
			if s.Tok == token.CONTINUE {
				k = exitContinue
			}
			return r.leave(k, s)
		}
	case *ast.BlockStmt:
		r.rewriteList(s.List, inLoop)
	case *ast.IfStmt:
		r.rewriteList(s.Body.List, inLoop)
		if s.Else != nil {
			s.Else = r.rewrite(s.Else, inLoop)
		}
	case *ast.ForStmt:
		r.rewriteList(s.Body.List, inLoop || r.t.userLoops[s])
	case *ast.RangeStmt:
		r.rewriteList(s.Body.List, inLoop || r.t.userLoops[s])
	case *ast.LabeledStmt:
		s.Stmt = r.rewrite(s.Stmt, inLoop)
	case *ast.SwitchStmt:
		r.rewriteList(s.Body.List, inLoop)
	case *ast.TypeSwitchStmt:
		r.rewriteList(s.Body.List, inLoop)
	case *ast.CaseClause:
		r.rewriteList(s.Body, inLoop)
	}
	return stmt
}

// leave is the statement standing for left, which leaves the statements the
// way k names: store, then the record of k, then the return from the literal.
func (r *releaseScope) leave(k int, left ast.Stmt, store ...ast.Stmt) ast.Stmt {
	if r.leaving[k] == nil {
		r.leaving[k] = left
	}
	return &ast.BlockStmt{List: append(store, assignStmt(r.exit, intLit(k)), &ast.ReturnStmt{})}
}

// anyOutsideFuncLits reports whether a node of stmts outside any function
// literal satisfies match.
func anyOutsideFuncLits(stmts []ast.Stmt, match func(ast.Node) bool) bool {
	found := false
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			if _, lit := n.(*ast.FuncLit); lit || found {
				return false
			}
			found = n != nil && match(n)
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// assignStmt is `name = value`.
func assignStmt(name string, value ast.Expr) ast.Stmt {
	return &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(name)}, Tok: token.ASSIGN, Rhs: []ast.Expr{value}}
}
