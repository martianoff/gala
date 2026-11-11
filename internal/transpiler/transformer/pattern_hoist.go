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

// siblingChain joins the sub-patterns of one pattern that sit side by side —
// tuple elements, a constructor's arguments, a sequence's elements — tested
// left to right: once one fails, the ones after it do not run. A sub-pattern
// that runs code (a user extractor's Unapply, a sealed view) after one that may
// fail runs under a flag holding whether every sub-pattern before it matched:
//
//	_tmp_5 := _subj.V1.Get() == 0
//	var _tmp_6 bool
//	if _tmp_5 {
//	    _tmp_6 = NonZeroDiv{}.Unapply(_subj.V2.Get())
//	    _tmp_5 = _tmp_6
//	}
//
// The names it declares are hoisted out of the guard, as a nested
// sub-pattern's are (see hoistPatternDecls). A sub-pattern that only reads
// values, or that no failing one precedes, runs as it is, and the conditions
// are joined with `&&`.
type siblingChain struct {
	t     *galaASTTransformer
	stmts []ast.Stmt
	conds []ast.Expr
	// ok is the flag, "" until a sub-pattern needs it.
	ok string
}

// add appends a sub-pattern lowered to cond and stmts. subject/subjectType
// name the temp it was lowered against, as hoistPatternDecls takes them.
func (c *siblingChain) add(cond ast.Expr, stmts []ast.Stmt, subject string, subjectType patternType) error {
	if len(stmts) == 0 || len(c.conds) == 0 || !c.t.runsCode(stmts) {
		c.stmts = append(c.stmts, stmts...)
		if !isLiteralTrue(cond) {
			c.conds = append(c.conds, cond)
		}
		return nil
	}
	decls, guarded, err := c.t.hoistPatternDecls(stmts, subject, subjectType)
	if err != nil {
		return err
	}
	if c.ok == "" {
		c.ok = c.t.nextTempVar()
		c.stmts = append(c.stmts, c.t.patternDefine([]string{c.ok}, []patternType{boolPatternType}, andAll(c.conds)))
	} else if len(c.conds) > 1 {
		c.stmts = append(c.stmts, assignStmt(c.ok, andAll(c.conds)))
	}
	if !isLiteralTrue(cond) {
		guarded = append(guarded, assignStmt(c.ok, cond))
	}
	c.stmts = append(c.stmts, decls...)
	c.stmts = append(c.stmts, &ast.IfStmt{Cond: ast.NewIdent(c.ok), Body: &ast.BlockStmt{List: guarded}})
	c.conds = []ast.Expr{ast.NewIdent(c.ok)}
	return nil
}

// result is the joined condition and the statements that run before it, as
// a pattern lowering returns them.
func (c *siblingChain) result() (ast.Expr, []ast.Stmt, error) {
	return andAll(c.conds), c.stmts, nil
}

// guardSubPatterns returns the joined condition of the sub-patterns of chain
// and their statements, run under `if ok { ... }` with the names they declare
// hoisted before it (see hoistPatternDecls). subject/subjectType name the
// guarded temp they read.
func (t *galaASTTransformer) guardSubPatterns(chain *siblingChain, ok, subject string, subjectType patternType) (ast.Expr, []ast.Stmt, error) {
	cond, stmts, _ := chain.result()
	decls, guarded, err := t.hoistPatternDecls(stmts, subject, subjectType)
	if err != nil {
		return nil, nil, err
	}
	return cond, assembleSeqPatternGuardBlock(decls, ok, guarded), nil
}

// andAll is conds joined with `&&`, leaving out the literal `true`s; `true`
// when nothing is left.
func andAll(conds []ast.Expr) ast.Expr {
	var cond ast.Expr
	for _, c := range conds {
		switch {
		case isLiteralTrue(c):
		case cond == nil:
			cond = c
		default:
			cond = &ast.BinaryExpr{X: cond, Op: token.LAND, Y: c}
		}
	}
	if cond == nil {
		return ast.NewIdent("true")
	}
	return cond
}

// runsCode reports whether stmts call anything but the reads a pattern makes
// of a value it already holds: an Immutable field's or an Option's `Get()`, a
// sequence element's `Get(i)` (behind the size check), `IsDefined()`, std.As,
// a conversion to `any`, and a sealed case's generated Unapply (patternReads).
func (t *galaASTTransformer) runsCode(stmts []ast.Stmt) bool {
	found := false
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && !found && !t.patternReads[call] && !isPatternRead(call) {
				found = true
			}
			return !found
		})
	}
	return found
}

// isPatternRead reports whether call is one of the reads runsCode allows by
// its shape.
func isPatternRead(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "any" && len(call.Args) == 1
	case *ast.SelectorExpr:
		return fun.Sel.Name == "Get" || len(call.Args) == 0 && fun.Sel.Name == "IsDefined"
	case *ast.IndexExpr:
		switch as := fun.X.(type) {
		case *ast.SelectorExpr:
			return as.Sel.Name == "As"
		case *ast.Ident:
			return as.Name == "As"
		}
	}
	return false
}
