package transformer

import (
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"

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
// `return` that would leave only the construct is GALA-E0069
// (checkTrappedReturns), and a `break` / `continue` GALA-E0059.

// hoistTarget is the variable a construct lowered as statements stores its
// value in.
type hoistTarget struct {
	name string
}

// hoistedValue is a construct lowered as statements: stmts store its value, of
// type typ, in its target.
type hoistedValue struct {
	stmts []ast.Stmt
	typ   transpiler.Type
}

// escapesConstruct reports whether tree holds a `return`, `break` or
// `continue` outside any lambda: one that, in a construct lowered to a
// function literal, could not reach the function or loop it is written for. A
// `break` / `continue` inside a `for` loop of tree's own controls that loop.
func escapesConstruct(tree antlr.Tree) bool {
	return escapes(tree, false)
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
		if _, ok := loopControlToken(n.GetText()); ok && !inLoop {
			return true
		}
		return false
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
		return escapesConstruct(f.ifExpr)
	case f.match != nil:
		for _, cc := range f.match.AllCaseClause() {
			if escapesConstruct(cc) {
				return true
			}
		}
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
	target := &hoistTarget{name: t.nextTempVar()}
	expr, err := t.lowerAgainst(exprs[0], slot{typ: declared, hoist: target}, true)
	if err != nil {
		return nil, err
	}
	hv, ok := t.takeHoisted(expr)
	if !ok {
		return []ast.Expr{expr}, nil
	}
	decl := &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(target.name)},
		Type:  t.typeToExpr(hv.typ),
	}}}}
	t.addVar(target.name, hv.typ)
	t.hoistedPre = append([]ast.Stmt{decl}, hv.stmts...)
	return []ast.Expr{ast.NewIdent(target.name)}, nil
}

// hoistedResult records stmts, storing a value of type typ in target, as the
// lowering of a construct and returns the placeholder expression that stands
// for it until its consumer takes it (takeHoisted).
func (t *galaASTTransformer) hoistedResult(target *hoistTarget, stmts []ast.Stmt, typ transpiler.Type) ast.Expr {
	ph := ast.NewIdent(target.name)
	if t.hoisted == nil {
		t.hoisted = make(map[*ast.Ident]hoistedValue)
	}
	// The arm that holds this construct reads its value's type off the
	// placeholder (see getExprTypeNameManual).
	t.hoisted[ph] = hoistedValue{stmts: stmts, typ: typ}
	return ph
}

// takeHoisted returns the construct expr stands for, if it is a placeholder
// hoistedResult returned, and forgets it.
func (t *galaASTTransformer) takeHoisted(expr ast.Expr) (hoistedValue, bool) {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = p.X
	}
	ident, ok := expr.(*ast.Ident)
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
// synthesized arm-tail `return X` into the store of X in target: the
// statements of X when X is itself a hoisted construct, `target = X`
// otherwise. A user `return` is left as it is: it leaves the function.
func (t *galaASTTransformer) storeArmValues(stmts []ast.Stmt, target *hoistTarget) []ast.Stmt {
	out := make([]ast.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.ReturnStmt:
			if len(n.Results) == 1 && t.isSynthesizedArmReturn(n) {
				out = append(out, t.storeValue(n.Results[0], target)...)
				continue
			}
			out = append(out, n)
		case *ast.BlockStmt:
			out = append(out, &ast.BlockStmt{List: t.storeArmValues(n.List, target)})
		case *ast.IfStmt:
			newIf := &ast.IfStmt{Init: n.Init, Cond: n.Cond}
			if n.Body != nil {
				newIf.Body = &ast.BlockStmt{List: t.storeArmValues(n.Body.List, target)}
			}
			switch e := n.Else.(type) {
			case *ast.BlockStmt:
				newIf.Else = &ast.BlockStmt{List: t.storeArmValues(e.List, target)}
			case *ast.IfStmt:
				newIf.Else = t.storeArmValues([]ast.Stmt{e}, target)[0]
			default:
				newIf.Else = n.Else
			}
			out = append(out, newIf)
		default:
			out = append(out, s)
		}
	}
	return out
}

// storeValue is the store of value in target: the statements of a hoisted
// construct, which store their own value, or `target = value`.
func (t *galaASTTransformer) storeValue(value ast.Expr, target *hoistTarget) []ast.Stmt {
	if hv, ok := t.takeHoisted(value); ok {
		return []ast.Stmt{&ast.BlockStmt{List: hv.stmts}}
	}
	return []ast.Stmt{&ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(target.name)},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{value},
	}}
}
