package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// patternType yields the Go type of a name a pattern's `:=` declares. It is
// built only when hoistPatternDecls writes that type out: building a type
// expression records the imports it names, and a type the generated Go never
// writes must not keep an import (a package the GALA file may not import).
// A nil patternType, or one yielding nil, means unknown.
type patternType func() ast.Expr

// exprType is the patternType of a type expression already built.
func exprType(e ast.Expr) patternType {
	if e == nil {
		return nil
	}
	return func() ast.Expr { return e }
}

// boolPatternType is the patternType of a pattern's `ok` flag.
var boolPatternType = exprType(ast.NewIdent("bool"))

// lazyKnownType is the patternType of typ, built with knownTypeExpr.
func (t *galaASTTransformer) lazyKnownType(typ transpiler.Type) patternType {
	return func() ast.Expr { return t.knownTypeExpr(typ) }
}

// lazyType is the patternType of typ, built with typeToExpr.
func (t *galaASTTransformer) lazyType(typ transpiler.Type) patternType {
	return func() ast.Expr { return t.typeToExpr(typ) }
}

// patternDefine builds `lhs... := rhs` for pattern lowering and records the Go
// type of each name it declares, so that hoistPatternDecls can later split it
// into a declaration and an assignment.
func (t *galaASTTransformer) patternDefine(lhs []string, types []patternType, rhs ast.Expr) *ast.AssignStmt {
	idents := make([]ast.Expr, len(lhs))
	for i, name := range lhs {
		idents[i] = ast.NewIdent(name)
	}
	stmt := &ast.AssignStmt{Lhs: idents, Tok: token.DEFINE, Rhs: []ast.Expr{rhs}}
	if t.patternDefineTypes == nil {
		t.patternDefineTypes = make(map[*ast.AssignStmt][]patternType)
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

// optionPayloadDefine builds `name := option.Value.Get()`, the payload of a std
// Option read from its Some case's field: its zero value when the Option is
// None. Unlike `var name T` it never names T, whose package the file may not
// import (Bazel's strict deps only allow the source's imports). The field is
// read as the analyzer laid Option out. typ is recorded for hoistPatternDecls.
func (t *galaASTTransformer) optionPayloadDefine(name, option string, typ patternType) (*ast.AssignStmt, error) {
	optionName := registry.StdPackageName + "." + transpiler.TypeOption
	meta := t.getTypeMeta(optionName)
	if meta != nil {
		for _, v := range meta.SealedVariants {
			if v.Name != "Some" || len(v.GoFieldNames) != 1 {
				continue
			}
			var read ast.Expr = &ast.SelectorExpr{X: ast.NewIdent(option), Sel: ast.NewIdent(v.GoFieldNames[0])}
			if i := slices.Index(meta.FieldNames, v.GoFieldNames[0]); i >= 0 && i < len(meta.ImmutFlags) && meta.ImmutFlags[i] {
				read = &ast.CallExpr{Fun: &ast.SelectorExpr{X: read, Sel: ast.NewIdent("Get")}}
			}
			return t.patternDefine([]string{name}, []patternType{typ}, read), nil
		}
	}
	return nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, "internal error: no field layout for case Some of "+optionName)
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
func (t *galaASTTransformer) hoistPatternDecls(stmts []ast.Stmt, subject string, subjectType patternType) (decls []ast.Stmt, guarded []ast.Stmt, err error) {
	// Types of the subject and of names declared so far in stmts, so
	// `name := temp` can take the temp's declared type.
	known := map[string]patternType{subject: subjectType}
	for _, s := range stmts {
		switch st := s.(type) {
		case *ast.DeclStmt:
			decls = append(decls, st)
			if gen, ok := st.Decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				for _, spec := range gen.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && vs.Type != nil {
						for _, n := range vs.Names {
							known[n.Name] = exprType(vs.Type)
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
				if i < len(types) && types[i] != nil {
					typ = types[i]()
				}
				if typ == nil && len(st.Lhs) == 1 && len(st.Rhs) == 1 {
					if src, ok := st.Rhs[0].(*ast.Ident); ok && known[src.Name] != nil {
						typ = known[src.Name]()
					}
				}
				if typ == nil {
					return nil, nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, fmt.Sprintf("cannot determine the type of pattern binding '%s'", ident.Name))
				}
				known[ident.Name] = exprType(typ)
				decls = append(decls, seqVarDecl(ident.Name, typ))
			}
			guarded = append(guarded, &ast.AssignStmt{Lhs: st.Lhs, Tok: token.ASSIGN, Rhs: st.Rhs})
		default:
			guarded = append(guarded, st)
		}
	}
	return decls, guarded, nil
}
