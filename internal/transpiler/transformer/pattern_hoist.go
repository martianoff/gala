package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// patternDefine builds `lhs... := rhs` for pattern lowering and records the Go
// type of each name it declares (a nil entry means unknown), so that
// hoistPatternDecls can later split it into a declaration and an assignment.
func (t *galaASTTransformer) patternDefine(lhs []string, types []ast.Expr, rhs ast.Expr) *ast.AssignStmt {
	idents := make([]ast.Expr, len(lhs))
	for i, name := range lhs {
		idents[i] = ast.NewIdent(name)
	}
	stmt := &ast.AssignStmt{Lhs: idents, Tok: token.DEFINE, Rhs: []ast.Expr{rhs}}
	if t.patternDefineTypes == nil {
		t.patternDefineTypes = make(map[*ast.AssignStmt][]ast.Expr)
	}
	t.patternDefineTypes[stmt] = types
	return stmt
}

// spliceStmts returns stmts with ins inserted before index at.
func spliceStmts(stmts []ast.Stmt, at int, ins []ast.Stmt) []ast.Stmt {
	if len(ins) == 0 {
		return stmts
	}
	out := make([]ast.Stmt, 0, len(stmts)+len(ins))
	out = append(out, stmts[:at]...)
	out = append(out, ins...)
	return append(out, stmts[at:]...)
}

// knownTypeExpr is typeToExpr for a type that is actually known: it returns
// nil (unknown) instead of `any` for a missing or unusable type, so a hoisted
// declaration never silently erases a binding's type.
func (t *galaASTTransformer) knownTypeExpr(typ transpiler.Type) ast.Expr {
	if typ == nil || transpiler.IsUnusable(typ) {
		return nil
	}
	return t.typeToExpr(typ)
}

// hoistPatternDecls splits a sub-pattern's lowered statements so they can run
// under a guard while the names they declare stay visible after it. It returns
// the declarations to emit BEFORE the guard (every top-level `var` plus a
// `var name T` for each name a top-level `:=` declares) and the statements to
// run INSIDE it, with each `:=` turned into `=`.
//
// A sub-pattern lowered against a guarded temp (a sequence element, an
// extractor's payload) must not run when the guard fails: the temp then holds
// a zero value, and reading a field through a nil pointer or calling a user
// Unapply on it can panic or have effects for an arm that was never selected.
//
// subject/subjectType name the guarded temp the sub-pattern was lowered
// against, so a bare binding of it (`name := subject`) takes its declared type.
func (t *galaASTTransformer) hoistPatternDecls(stmts []ast.Stmt, subject string, subjectType ast.Expr) (decls []ast.Stmt, guarded []ast.Stmt, err error) {
	// Types of the subject and of names declared so far in stmts, so
	// `name := temp` can take the temp's declared type.
	known := map[string]ast.Expr{subject: subjectType}
	for _, s := range stmts {
		switch st := s.(type) {
		case *ast.DeclStmt:
			decls = append(decls, st)
			if gen, ok := st.Decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				for _, spec := range gen.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && vs.Type != nil {
						for _, n := range vs.Names {
							known[n.Name] = vs.Type
						}
					}
				}
			}
		case *ast.AssignStmt:
			if st.Tok != token.DEFINE {
				guarded = append(guarded, st)
				continue
			}
			types := t.patternDefineTypes[st]
			for i, l := range st.Lhs {
				ident, ok := l.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				var typ ast.Expr
				if i < len(types) {
					typ = types[i]
				}
				if typ == nil && len(st.Lhs) == 1 && len(st.Rhs) == 1 {
					if src, ok := st.Rhs[0].(*ast.Ident); ok {
						typ = known[src.Name]
					}
				}
				if typ == nil {
					return nil, nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, fmt.Sprintf("cannot determine the type of pattern binding '%s'", ident.Name))
				}
				known[ident.Name] = typ
				decls = append(decls, seqVarDecl(ident.Name, typ))
			}
			guarded = append(guarded, &ast.AssignStmt{Lhs: st.Lhs, Tok: token.ASSIGN, Rhs: st.Rhs})
		default:
			guarded = append(guarded, st)
		}
	}
	return decls, guarded, nil
}
