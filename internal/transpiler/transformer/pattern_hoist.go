package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// patternType is the Go type of a name a pattern's `:=` declares: a type
// expression already built, or the GALA type to build one from. The
// expression is built only when hoistPatternDecls writes that type out:
// building it records the imports it names, and a type the generated Go never
// writes must not keep an import (a package the GALA file may not import).
// A nil patternType, or one that builds to nil, means unknown.
type patternType = *patternTypeOf

type patternTypeOf struct {
	expr ast.Expr
	typ  transpiler.Type
	// known builds typ with knownTypeExpr instead of typeToExpr.
	known bool
}

// exprType is the patternType of a type expression already built.
func exprType(e ast.Expr) patternType {
	if e == nil {
		return nil
	}
	return &patternTypeOf{expr: e}
}

// boolPatternType is the patternType of a pattern's `ok` flag.
var boolPatternType = exprType(ast.NewIdent("bool"))

// lazyKnownType is the patternType of typ, built with knownTypeExpr.
func (t *galaASTTransformer) lazyKnownType(typ transpiler.Type) patternType {
	return &patternTypeOf{typ: typ, known: true}
}

// lazyType is the patternType of typ, built with typeToExpr.
func (t *galaASTTransformer) lazyType(typ transpiler.Type) patternType {
	return &patternTypeOf{typ: typ}
}

// buildPatternType is p's type expression, nil when it is unknown.
func (t *galaASTTransformer) buildPatternType(p patternType) ast.Expr {
	switch {
	case p == nil:
		return nil
	case p.expr != nil:
		return p.expr
	case p.known:
		return t.knownTypeExpr(p.typ)
	}
	return t.typeToExpr(p.typ)
}

// spellable reports whether p is a known type this file can write out without
// a new import: an expression already built (bool, or a type the pattern
// wrote), or a type every package of which the file imports (see
// namesOnlyImportedPackages). A value matched in this file can have a type
// from a package the file reaches only through another one, and an import
// of that package is one Bazel's strict dependencies reject.
func (t *galaASTTransformer) spellable(p patternType) bool {
	switch {
	case p == nil:
		return false
	case p.expr != nil:
		return true
	case !p.knowsType():
		return false
	}
	return t.namesOnlyImportedPackages(p.typ)
}

// knowsType reports whether p's GALA type is one a Go type can be built from.
func (p *patternTypeOf) knowsType() bool {
	return p.typ != nil && !transpiler.IsUnusable(p.typ)
}

// pointerness tells whether a value of type p is a pointer: yes, no, or
// unknown (ok false).
func pointerness(p patternType) (pointer, ok bool) {
	switch {
	case p == nil:
		return false, false
	case p.expr != nil:
		_, ptr := p.expr.(*ast.StarExpr)
		return ptr, true
	case !p.knowsType():
		return false, false
	}
	_, ptr := p.typ.(transpiler.PointerType)
	return ptr, true
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
		t.patternDefineTypes = make(map[ast.Node][]patternType)
	}
	t.patternDefineTypes[stmt] = types
	return stmt
}

// sameTypeValue returns a value of rhs's type that hoistPatternDecls can
// declare a name from before the guard, in place of the type, or nil when it
// knows none. rhs is what the guard later assigns the name; the value is only
// a placeholder for it, so it runs none of rhs's code and cannot panic:
//
//   - a name declared before the guard (known) is that name;
//   - a field's read, `base.F` or an Immutable field's `base.F.Get()`, is
//     that read of base, a name declared before the guard, through a nil-safe
//     dereference when base is a pointer;
//   - an extractor's `E{}.Unapply(x)` is the zero value of its result, typed
//     from the method value `E{}.Unapply`, which is not called;
//   - a sequence element's `xs.Get(i)` is the zero value of the element, typed
//     from the method value `xs.Get`. The sequence pattern reads xs, a plain
//     read, before the guard already (its size check);
//   - any other call of a method with at most one argument on a name,
//     `ext.Unapply(x)` or `v.MatchView()`, is the zero value of its result,
//     typed from the method value (see methodValue), which is not called;
//   - a call `h(x)` of a generated Unapply helper (see unapplyHelper) with a
//     name x is the zero value of its result, typed from h and x
//     without calling h.
//
// Pattern lowering writes `.F.Get()` only for an Immutable field, whose Get
// has no effect, as runsCode assumes too.
func (t *galaASTTransformer) sameTypeValue(rhs ast.Expr, known map[string]patternType) ast.Expr {
	switch e := rhs.(type) {
	case *ast.Ident:
		if _, declared := known[e.Name]; declared {
			return t.localRef(e.Name)
		}
	case *ast.SelectorExpr:
		if recv := t.knownFieldBase(e, known); recv != nil {
			return &ast.SelectorExpr{X: recv, Sel: ast.NewIdent(e.Sel.Name)}
		}
	case *ast.CallExpr:
		switch fun := e.Fun.(type) {
		case *ast.Ident:
			if len(e.Args) != 1 || !t.isUnapplyHelper(fun.Name) {
				return nil
			}
			// The argument is only read, so it must be a name: a field read
			// may go through a pointer the guard has not checked.
			if arg, ok := e.Args[0].(*ast.Ident); ok {
				return t.helperCall(zeroApplyHelper, ast.NewIdent(fun.Name), t.localRef(arg.Name))
			}
		case *ast.SelectorExpr:
			switch {
			case fun.Sel.Name == "Get" && len(e.Args) == 0:
				field, ok := fun.X.(*ast.SelectorExpr)
				if !ok {
					return nil
				}
				if recv := t.knownFieldBase(field, known); recv != nil {
					return &ast.CallExpr{Fun: &ast.SelectorExpr{
						X:   &ast.SelectorExpr{X: recv, Sel: ast.NewIdent(field.Sel.Name)},
						Sel: ast.NewIdent("Get"),
					}}
				}
				return nil
			case fun.Sel.Name == "Unapply" && len(e.Args) == 1:
				if lit, ok := fun.X.(*ast.CompositeLit); ok {
					return t.helperCall(zeroResultHelper, &ast.SelectorExpr{
						X: &ast.CompositeLit{Type: lit.Type}, Sel: ast.NewIdent("Unapply"),
					})
				}
			case fun.Sel.Name == "Get" && len(e.Args) == 1:
				if isPlainRead(fun.X) {
					return t.helperCall(zeroResultHelper, &ast.SelectorExpr{X: fun.X, Sel: ast.NewIdent("Get")})
				}
			}
			if len(e.Args) > 1 {
				return nil
			}
			if mv := t.methodValue(fun, known); mv != nil {
				if len(e.Args) == 0 {
					return t.helperCall(zeroResult0Helper, mv)
				}
				return t.helperCall(zeroResultHelper, mv)
			}
		}
	}
	return nil
}

// isUnapplyHelper reports whether name is an Unapply helper this file
// declares (see unapplyHelper), not one of the pattern helpers filed beside
// them under their `_gala_` base names (see patternHelper).
func (t *galaASTTransformer) isUnapplyHelper(name string) bool {
	for key, helper := range t.unapplyHelpers {
		if helper == name && !strings.HasPrefix(key, "_gala_") {
			return true
		}
	}
	return false
}

// knownFieldBase is the receiver a placeholder reads field.Sel of: field's
// base, a name declared before the guard whose pointerness is known, through
// a nil-safe dereference when it is a pointer. nil otherwise.
func (t *galaASTTransformer) knownFieldBase(field *ast.SelectorExpr, known map[string]patternType) ast.Expr {
	base, ok := field.X.(*ast.Ident)
	if !ok {
		return nil
	}
	p, declared := known[base.Name]
	pointer, sure := pointerness(p)
	if !declared || !sure {
		return nil
	}
	var recv ast.Expr = t.localRef(base.Name)
	if pointer {
		recv = t.helperCall(derefHelper, recv)
	}
	return recv
}

// methodValue is the method value `recv.M` of fun, recv a name, made safe to
// evaluate before the guard, where the name may still hold a zero value: recv
// is a value of a GALA type, or a pointer to one, read through a nil-safe
// dereference when M has a value receiver. nil for any other receiver, such
// as an interface value, which may be nil, or one of an unknown type.
func (t *galaASTTransformer) methodValue(fun *ast.SelectorExpr, known map[string]patternType) ast.Expr {
	id, ok := fun.X.(*ast.Ident)
	if !ok {
		return nil
	}
	name := fun.Sel.Name
	var recv ast.Expr = t.localRef(id.Name)
	var typ transpiler.Type
	if p := known[id.Name]; p == nil {
		typ = t.getExprTypeName(id)
	} else if p.knowsType() {
		typ = p.typ
	}
	if typ == nil || transpiler.IsUnusable(typ) {
		return nil
	}
	ptr, isPointer := typ.(transpiler.PointerType)
	elem := typ
	if isPointer {
		elem = ptr.Elem
	}
	_, meta, _, ok := t.receiverTypeInfo(t.methodReceiverType(elem, name))
	if !ok || meta == nil || meta.IsInterface || meta.Methods[name] == nil {
		return nil
	}
	if isPointer && !meta.Methods[name].PtrReceiver {
		recv = t.helperCall(derefHelper, recv)
	}
	return &ast.SelectorExpr{X: recv, Sel: ast.NewIdent(name)}
}

// isPlainRead reports whether e only reads: a name, a field of a plain read,
// or an Immutable field's `Get()` of one.
func isPlainRead(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return isPlainRead(x.X)
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Get" && len(x.Args) == 0 && isPlainRead(sel.X)
	}
	return false
}

// localRef is a reference to the local name, recorded so it follows the
// local when renameShadowingLocals renames it.
func (t *galaASTTransformer) localRef(name string) *ast.Ident {
	id := ast.NewIdent(name)
	if shadowsGeneratedName(name) {
		t.recordLocalRef(id)
	}
	return id
}

// helperCall is a call of the pattern helper named base. It only reads, so
// runsCode does not take it for code a guard must hold back.
func (t *galaASTTransformer) helperCall(base string, args ...ast.Expr) *ast.CallExpr {
	call := &ast.CallExpr{Fun: ast.NewIdent(t.patternHelper(base)), Args: args}
	if t.patternReads == nil {
		t.patternReads = make(map[*ast.CallExpr]bool)
	}
	t.patternReads[call] = true
	return call
}

// The generic helpers sameTypeValue calls.
const (
	// derefHelper is `func[T any](p *T) (v T)`: *p, or the zero value when
	// p is nil.
	derefHelper = "_gala_deref"
	// zeroResultHelper is `func[A, R any](func(A) R) (r R)`: the zero value
	// of the result of the function it is given, which it does not call.
	zeroResultHelper = "_gala_zero_result"
	// zeroResult0Helper is `func[R any](func() R) (r R)`, zeroResultHelper
	// for a function of no arguments.
	zeroResult0Helper = "_gala_zero_result0"
	// zeroApplyHelper is `func[A, R any](func(A) R, A) (r R)`: the zero value
	// of the result of the function it is given, applied to a value of the
	// argument's type, which it does not call. The argument lets Go infer
	// the type arguments of a generic function.
	zeroApplyHelper = "_gala_zero_apply"
)

// patternHelper returns the Go name of the helper named base in this file,
// declaring it on first use. Like an Unapply helper (see unapplyHelper) it is
// one of this file's own: base carries the reserved `_gala_` prefix, so no
// GALA name is the same, and the file's suffix keeps two files of one
// package from both declaring it.
func (t *galaASTTransformer) patternHelper(base string) string {
	if name, ok := t.unapplyHelpers[base]; ok {
		return name
	}
	name := base + t.codecFileSuffix()
	id := ast.NewIdent
	anyParam := func(names ...string) *ast.Field {
		idents := make([]*ast.Ident, len(names))
		for i, n := range names {
			idents[i] = id(n)
		}
		return &ast.Field{Names: idents, Type: id("any")}
	}
	var decl *ast.FuncDecl
	switch base {
	case derefHelper:
		decl = &ast.FuncDecl{
			Name: id(name),
			Type: &ast.FuncType{
				TypeParams: &ast.FieldList{List: []*ast.Field{anyParam("T")}},
				Params:     &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{id("p")}, Type: &ast.StarExpr{X: id("T")}}}},
				Results:    &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{id("v")}, Type: id("T")}}},
			},
			Body: &ast.BlockStmt{List: []ast.Stmt{
				&ast.IfStmt{
					Cond: &ast.BinaryExpr{X: id("p"), Op: token.NEQ, Y: id("nil")},
					Body: &ast.BlockStmt{List: []ast.Stmt{assignStmt("v", &ast.StarExpr{X: id("p")})}},
				},
				&ast.ReturnStmt{},
			}},
		}
	case zeroResultHelper, zeroResult0Helper, zeroApplyHelper:
		// func[A, R any](func(A) R) (r R), without A for zeroResult0Helper
		// and with an A argument for zeroApplyHelper.
		typeParams, fnParams := anyParam("A", "R"), []*ast.Field{{Type: id("A")}}
		if base == zeroResult0Helper {
			typeParams, fnParams = anyParam("R"), nil
		}
		params := []*ast.Field{{Type: &ast.FuncType{
			Params:  &ast.FieldList{List: fnParams},
			Results: &ast.FieldList{List: []*ast.Field{{Type: id("R")}}},
		}}}
		if base == zeroApplyHelper {
			params = append(params, &ast.Field{Type: id("A")})
		}
		decl = &ast.FuncDecl{
			Name: id(name),
			Type: &ast.FuncType{
				TypeParams: &ast.FieldList{List: []*ast.Field{typeParams}},
				Params:     &ast.FieldList{List: params},
				Results:    &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{id("r")}, Type: id("R")}}},
			},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{}}},
		}
	default:
		// invariant: pattern-helper — base is one of the constants above.
		panic("no pattern helper " + base)
	}
	t.unapplyHelperDecls = append(t.unapplyHelperDecls, decl)
	t.unapplyHelpers[base] = name
	return name
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
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					// A name an inner hoist declared from a value has the
					// type it recorded (see below).
					typ := exprType(vs.Type)
					if types := t.patternDefineTypes[vs]; vs.Type == nil && len(types) == 1 {
						typ = types[0]
					}
					for _, n := range vs.Names {
						known[n.Name] = typ
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
				var p patternType
				if i < len(types) {
					p = types[i]
				}
				var rhs ast.Expr // the value it is declared from, when it is the only one
				if len(st.Lhs) == 1 && len(st.Rhs) == 1 {
					rhs = st.Rhs[0]
				}
				spelled := t.spellable(p)
				if src, ok := rhs.(*ast.Ident); ok && !spelled && known[src.Name] != nil {
					// A copy of a name has that name's type.
					p = known[src.Name]
					spelled = t.spellable(p)
				}
				var like ast.Expr
				if !spelled {
					// The type is unknown, or names a package this file does
					// not import: the name takes it from a value of that type
					// instead, which the guarded assignment then replaces.
					like = t.sameTypeValue(rhs, known)
				}
				if like != nil {
					known[ident.Name] = p
					decl := seqVarDecl(ident.Name, nil, like)
					t.patternDefineTypes[decl.(*ast.DeclStmt).Decl.(*ast.GenDecl).Specs[0]] = []patternType{p}
					decls = append(decls, decl)
				} else if !spelled && p != nil && p.knowsType() && t.namesUnimportedGalaPackage(p.typ) {
					// Spelling the type would import a GALA package this file
					// does not, which Bazel's strict dependencies reject.
					return nil, nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, fmt.Sprintf(
						"cannot declare a value this pattern binds: its type %s is from a package this file does not import (hint: import the package that declares it)",
						displayType(p.typ)))
				} else if typ := t.buildPatternType(p); typ != nil {
					known[ident.Name] = exprType(typ)
					decls = append(decls, seqVarDecl(ident.Name, typ))
				} else {
					return nil, nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, fmt.Sprintf("cannot determine the type of pattern binding '%s'", ident.Name))
				}
				// The binding is now assigned, not declared, here; the
				// var declares it (see renameShadowingLocals).
				if shadowsGeneratedName(ident.Name) {
					t.recordLocalRef(ident)
				}
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
