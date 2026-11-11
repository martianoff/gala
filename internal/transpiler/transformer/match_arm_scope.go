package transformer

import (
	"go/ast"
	"go/token"
	"slices"
)

// A match lowers to an if-else chain, each case's pattern statements in a
// block in front of its `if`:
//
//	{
//	    _tmp_1 := Circle{}.Unapply(_subj)
//	    _tmp_r := _tmp_1.Value.Get()
//	    if _tmp_1.IsDefined() {
//	        r := _tmp_r
//	        ... body
//	    } else {
//	        ... the later cases
//	    }
//	}
//
// The later cases sit in that block too, so a name the pattern declares there
// would be in scope in them, hiding the name the user meant. The pattern
// therefore declares only the transpiler's own temps; each pattern variable
// is bound to the user's name inside the branches that may read it, the
// guard's and the body's (see scopeArmBindings). A guard that reads a pattern
// variable is evaluated in a block of its own, before the `if`:
//
//	var _tmp_2 bool
//	if _tmp_1.IsDefined() {
//	    r := _tmp_r
//	    _tmp_2 = r > 100
//	}
//	if _tmp_2 { r := _tmp_r; ... body } else { ... }

// binderTemp is a pattern variable and the temp its pattern stores it in.
type binderTemp struct {
	name, temp string
}

// scopeArmBindings builds a case's lowered arm, `{ stmts; if cond && guard {
// body } }`, with names, the pattern variables stmts declare, visible only in
// the guard and the body (see above). guard is nil when the case has none.
func (t *galaASTTransformer) scopeArmBindings(cond ast.Expr, stmts []ast.Stmt, names []string, guard ast.Expr, body []ast.Stmt) ast.Stmt {
	binders := hideBinders(stmts, names)
	bodyBlock := &ast.BlockStmt{List: t.bindIn(binders, body)}
	if guard == nil || !t.readsAnyBinder(binders, guard) {
		return armBlock(stmts, &ast.IfStmt{Cond: joinGuard(cond, guard), Body: bodyBlock})
	}
	matched := t.nextTempVar()
	guardBlock := &ast.BlockStmt{List: t.bindIn(binders, []ast.Stmt{assignStmt(matched, guard)})}
	var test ast.Stmt = guardBlock
	if !isLiteralTrue(cond) {
		test = &ast.IfStmt{Cond: cond, Body: guardBlock}
	}
	stmts = append(stmts, seqVarDecl(matched, ast.NewIdent("bool")), test)
	return armBlock(stmts, &ast.IfStmt{Cond: ast.NewIdent(matched), Body: bodyBlock})
}

// armBlock is `{ stmts; test }`, or test alone when there are no stmts.
func armBlock(stmts []ast.Stmt, test *ast.IfStmt) ast.Stmt {
	if len(stmts) == 0 {
		return test
	}
	return &ast.BlockStmt{List: append(stmts, test)}
}

// hideBinders renames each of names, the pattern variables stmts declare, to
// a temp of its own, `_tmp_<name>`, and returns them with their temps. A
// pattern only stores its variables, never reads them, so only the names it
// declares and assigns are renamed: a read of a name (a value of the enclosing
// scope, a type or a package of the same name) is left as it is.
func hideBinders(stmts []ast.Stmt, names []string) []binderTemp {
	temps := make(map[string]string, len(names))
	binders := make([]binderTemp, 0, len(names))
	for _, n := range names {
		if _, dup := temps[n]; !dup {
			temps[n] = "_tmp_" + n
			binders = append(binders, binderTemp{name: n, temp: temps[n]})
		}
	}
	if len(binders) == 0 {
		return nil
	}
	rename := func(id *ast.Ident) {
		if temp, ok := temps[id.Name]; ok {
			id.Name = temp
		}
	}
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				// A literal's own statements are no pattern's.
				return false
			case *ast.AssignStmt:
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						rename(id)
					}
				}
			case *ast.ValueSpec:
				for _, id := range x.Names {
					rename(id)
				}
			}
			return true
		})
	}
	return binders
}

// bindIn is stmts preceded by `name := temp` for each binder stmts may read.
// A binder stmts name without reading it (a lambda parameter of that name, say)
// is still bound, and marked used, so Go never sees an unused variable or an
// undefined one. stmts go in a block of their own when they declare a binder's
// name again.
func (t *galaASTTransformer) bindIn(binders []binderTemp, stmts []ast.Stmt) []ast.Stmt {
	var binds []ast.Stmt
	nodes := make([]ast.Node, len(stmts))
	for i, s := range stmts {
		nodes[i] = s
	}
	refs := t.collectReferencedIdents(nodes)
	redeclared := false
	for _, b := range binders {
		if !refs[b.name] {
			continue
		}
		binds = append(binds, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(b.name)}, Tok: token.DEFINE, Rhs: []ast.Expr{ast.NewIdent(b.temp)},
		})
		if !t.stmtsReadName(stmts, b.name) {
			binds = blankAssignTempVar(binds, b.name)
		}
		redeclared = redeclared || declaresName(stmts, b.name)
	}
	if len(binds) == 0 {
		return stmts
	}
	if redeclared {
		return append(binds, &ast.BlockStmt{List: stmts})
	}
	return append(binds, stmts...)
}

// readsAnyBinder reports whether guard names any of binders.
func (t *galaASTTransformer) readsAnyBinder(binders []binderTemp, guard ast.Expr) bool {
	refs := t.collectReferencedIdents([]ast.Node{guard})
	return slices.ContainsFunc(binders, func(b binderTemp) bool { return refs[b.name] })
}

// stmtsReadName is readsName, looking into the statements of a match or
// if-expression lowered as statements that stmts hold as a placeholder (see
// hoistedResult).
func (t *galaASTTransformer) stmtsReadName(stmts []ast.Stmt, name string) bool {
	if readsName(stmts, name) {
		return true
	}
	found := false
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && !found {
				if hv, ok := t.hoisted[id]; ok {
					found = t.stmtsReadName(hv.stmts, name)
				}
			}
			return !found
		})
	}
	return found
}

// declaresName reports whether one of stmts declares name in their own scope.
func declaresName(stmts []ast.Stmt, name string) bool {
	isName := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == name
	}
	for _, s := range stmts {
		switch x := s.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE && slices.ContainsFunc(x.Lhs, isName) {
				return true
			}
		case *ast.DeclStmt:
			if gen, ok := x.Decl.(*ast.GenDecl); ok {
				for _, spec := range gen.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && slices.ContainsFunc(vs.Names, func(id *ast.Ident) bool { return id.Name == name }) {
						return true
					}
				}
			}
		}
	}
	return false
}
