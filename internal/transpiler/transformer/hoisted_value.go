package transformer

import (
	"fmt"
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
// initializer of a local `val` or `var`, it is lowered as statements instead:
// a variable of the construct's type is declared before the declaration, each
// arm stores its value in it, and the declaration reads it. A `return`, `break`
// or `continue` in an arm then acts on the enclosing function or loop, as it
// does in a statement-position match.
//
// A construct is lowered this way only when it needs to be: when its arms hold
// such a statement outside any lambda (escapesConstruct). An arm whose value is
// itself such a construct stores into the same variable. Anywhere else, a
// `return` that would leave only the construct is GALA-E0069 and a `break` /
// `continue` GALA-E0059 (see branching_calls.go and loop_control.go).
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
		return escapesConstruct(t, f.ifExpr)
	case f.match != nil:
		return escapesConstruct(t, f.match.AllCaseClause()...)
	}
	return false
}

// lowerDeclarationInitializers lowers the initializers of a `val` or `var`
// declaring names names, with declared type declared (nil when none). The
// single initializer of a single local name is lowered as statements when
// hoistable: the declaration is then initialized with the variable they store
// its value in, and the statements are left in t.hoistedPre for
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
	decl := &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(target)},
		Type:  t.typeToExpr(hv.typ),
	}}}}
	t.addVar(target, hv.typ)
	t.hoistedPre = append([]ast.Stmt{decl}, hv.stmts...)
	return []ast.Expr{ast.NewIdent(target)}, nil
}

// hoistedResult records stmts, storing a value of type typ in target, as the
// lowering of a construct and returns the placeholder expression that stands
// for it until its consumer takes it (takeHoisted). The placeholder has the
// type of the value, for the arm that holds the construct.
func (t *galaASTTransformer) hoistedResult(target string, stmts []ast.Stmt, typ transpiler.Type) ast.Expr {
	ph := ast.NewIdent(target)
	if t.hoisted == nil {
		t.hoisted = make(map[*ast.Ident]hoistedValue)
	}
	t.hoisted[ph] = hoistedValue{stmts: stmts, typ: typ}
	t.exprTypeCache[ph] = typ
	return ph
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
	if ident, ok := value.(*ast.Ident); ok && !transpiler.IsUnusableOrAny(typ) {
		if vt := t.getType(ident.Name); vt != nil && vt.IsAny() {
			value = &ast.TypeAssertExpr{X: value, Type: t.typeToExpr(typ)}
		}
	}
	return []ast.Stmt{&ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(target)},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{value},
	}}
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
// path: they end in a source `return`, a `break` or `continue`, or an if/else
// every branch of which leaves.
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

// checkHoistedReturns rejects, in the branches of a construct of kind lowered
// as statements, a source `return` with a value when the enclosing function
// returns none. Lowered to a function literal, such a `return` used to give the
// construct its value; lowered as statements it leaves the function, so what
// was meant as the branch's value is GALA-E0069 here rather than Go's "too
// many return values".
func (t *galaASTTransformer) checkHoistedReturns(kind string, branches ...[]ast.Stmt) error {
	if t.returnSlot.fillable || !transpiler.IsUnusable(t.returnSlot.typ) && !t.returnSlot.typ.IsVoid() {
		return nil
	}
	var found error
	visit := func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if pos, user := t.userReturns[x]; user && len(x.Results) > 0 {
				found = galaerr.NewCodedSemanticError(
					galaerr.CodeReturnInBranchingValue,
					pos.line, pos.col,
					fmt.Sprintf("`return` with a value inside %s leaves the function, which returns nothing", withArticle(kind)),
					fmt.Sprintf("to give the %s this value, end the %s in it instead of `return`", kind, branchNoun(kind)))
			}
		}
		return true
	}
	for _, stmts := range branches {
		for _, s := range stmts {
			ast.Inspect(s, visit)
		}
	}
	return found
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
