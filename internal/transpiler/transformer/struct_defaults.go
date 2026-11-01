package transformer

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Shorthand struct fields may carry a default: `struct Cfg(Name string, Tries
// int = 3)`. The declaration reuses the grammar's `parameter` rule, so the
// default parses exactly as a function parameter's would, and the analyzer
// records its source text on TypeMetadata.FieldDefaults.
//
// This file lowers that record at the construction site. Call syntax — both
// `Cfg(Name = "a")` and `Cfg("a")` — is a constructor call, so it follows the
// same rule a function call already follows: an omitted parameter takes its
// default, and omitting one that has no default is an error. Before this, an
// omitted field took Go's zero value in silence, which made a declared default
// decorative and let a literal that simply forgot a field compile with a zero
// in it.
//
// Go-style literals (`Cfg{Name: "a"}`) keep Go's semantics and stay partial;
// they are not constructor calls and never consult defaults.
//
// The required-field half applies only to the shorthand form. A block-form
// struct (`type Cfg struct { ... }`) has no syntax for a field default, so
// requiring every field would leave no way to opt out and would break the
// partial construction the standard library itself relies on — HAMT nodes in
// collection_immutable set two of four fields and mean the rest to be zero.
// The shorthand's field list is a constructor signature; a block struct is a
// layout.

// fillOmittedStructFields returns the extra KeyValueExprs a construction needs
// for the fields it did not supply: a field's declared default, or else its
// type's default value (see defaultValue), and GALA-E0075 for a field whose
// type has none. With requireDeclared, a call of a shorthand struct instead
// reports the fields with no declared default it left out (GALA-E0045): its
// field list is a constructor signature.
//
// provided answers whether the call site gave a value for the field at index i;
// the named and positional paths compute it differently, so they pass their own
// — positional construction fills left to right, so its answer is just
// `i < len(args)`. Ordering follows the declaration order in fields, which keeps
// the emitted literal stable and readable.
func (t *galaASTTransformer) fillOmittedStructFields(
	typeName, resolvedTypeName string,
	fields []string,
	provided func(i int, fieldName string) bool,
	typeArgSubst map[string]ast.Expr,
	line, col int,
	requireDeclared bool,
	seen map[string]bool,
) ([]ast.Expr, error) {
	if seen == nil {
		seen = map[string]bool{}
	}
	meta := t.getTypeMeta(resolvedTypeName)
	if meta == nil {
		return nil, nil
	}
	defaults := meta.FieldDefaults
	immutFlags := t.structImmutFields[resolvedTypeName]
	fieldTypes := t.structFieldTypes[resolvedTypeName]
	var typeArgs map[string]transpiler.Type // converted on first use

	var missing []string
	var elts []ast.Expr
	// emit adds the value a construction gives field i.
	emit := func(i int, fieldName string, val ast.Expr) error {
		if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
			val = t.wrapImmutableFieldValue(val, fieldTypes[fieldName], typeArgSubst)
		}
		elts = append(elts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: val})
		return nil
	}
	for i, fieldName := range fields {
		// A field private to another package is that package's to set, with a
		// constructor it exports; here it keeps Go's zero value.
		if provided(i, fieldName) || !visibleFrom(fieldName, meta, t.packageName) {
			continue
		}
		if typeArgs == nil {
			typeArgs = t.typeArgTypes(typeArgSubst)
		}
		def, hasDefault := defaults[fieldName]
		if hasDefault && isZeroLiteralOf(def.Text, fieldTypes[fieldName]) {
			continue // Go's zero value is that default
		}
		if !hasDefault {
			if requireDeclared && meta.IsShorthand {
				missing = append(missing, fieldName)
				continue
			}
			fieldType := t.substituteConcreteTypes(fieldTypes[fieldName], meta.TypeParams, orderedTypeArgs(meta.TypeParams, typeArgs))
			val, err := t.defaultValue(fieldType, line, col, seen)
			if err != nil {
				return nil, fieldHasNoDefault(err, fieldName, typeName)
			}
			if val == nil {
				continue // Go's zero value is the field's default
			}
			if err := emit(i, fieldName, val); err != nil {
				return nil, err
			}
			continue
		}
		val, err := t.declaredFieldDefault(meta, fieldName, fieldTypes[fieldName], typeArgs, line, col)
		if err != nil {
			return nil, err
		}
		if err := emit(i, fieldName, val); err != nil {
			return nil, err
		}
	}

	if len(missing) > 0 {
		return nil, missingStructFieldsError(typeName, missing, defaults, fields, line, col)
	}
	return elts, nil
}

// declaredFieldDefault lowers the declared default of field (of type
// fieldType) of the struct meta describes, its type parameters bound to
// typeArgs. It is re-parsed and re-transformed per construction site, so a
// default like `time.Now()` is evaluated at each construction rather than once
// at declaration — the same contract function parameter defaults have. The
// field's declared type is the expected type, as it is for a value passed
// explicitly.
func (t *galaASTTransformer) declaredFieldDefault(meta *transpiler.TypeMetadata, field string, fieldType transpiler.Type, typeArgs map[string]transpiler.Type, line, col int) (ast.Expr, error) {
	src := defaultSource{
		DefaultExpr: meta.FieldDefaults[field],
		file:        meta.DefinedIn,
		pkg:         meta.Package,
		typeParams:  meta.TypeParams,
	}
	t.substituteDeclared(&src, fieldType, typeArgs)
	return t.transformDefaultExpr(src, line, col)
}

// unknownStructFieldError reports a named argument that matches no field.
//
// Without it the mistake surfaces only as its consequence: `Cfg(Nmae = "a")`
// drops the unmatched argument and then reports the missing "Name", which
// names the field the author thought they had supplied. Function calls already
// report `unknown parameter` for the same slip, and call-syntax construction is
// meant to follow the same rules.
func unknownStructFieldError(typeName string, unknown, fields []string, line, col int) error {
	sort.Strings(unknown)
	label := "field"
	if len(unknown) > 1 {
		label = "fields"
	}
	hint := fmt.Sprintf("%s declares: %s", typeName, strings.Join(fields, ", "))
	if len(fields) == 0 {
		// A Go struct whose fields are all unexported to this package.
		hint = fmt.Sprintf("%s has no field this package can set", typeName)
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeMissingStructField,
		line, col,
		fmt.Sprintf("unknown %s %s in construction of %q", label, quoteJoin(unknown), typeName),
		hint,
	)
}

// missingStructFieldsError reports the required fields a constructor call left
// out. It names every one of them rather than only the first, so a call site
// missing several fields takes one round trip instead of several, and closes
// with the fix that applies to the declaration rather than the call when the
// struct has no defaults at all.
func missingStructFieldsError(
	typeName string,
	missing []string,
	defaults map[string]transpiler.DefaultExpr,
	fields []string,
	line, col int,
) error {
	sort.Strings(missing)
	label := "field"
	if len(missing) > 1 {
		label = "fields"
	}
	named := quoteJoin(missing)

	hint := fmt.Sprintf("pass %s, or give the %s a default in the declaration (e.g. %s int = 0)",
		named, label, missing[0])
	if len(defaults) == 0 && len(missing) == len(fields) {
		// Every field is missing and none is defaulted — most likely the caller
		// meant a Go-style literal, which is allowed to be partial.
		hint += fmt.Sprintf("; a partial literal is written %s{...}", typeName)
	}

	return galaerr.NewCodedSemanticError(
		galaerr.CodeMissingStructField,
		line, col,
		fmt.Sprintf("missing required %s %s in construction of %q", label, named, typeName),
		hint,
	)
}

// quoteJoin renders a field-name list as `"a", "b" and "c"`.
func quoteJoin(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	return andJoin(q)
}

// andJoin renders a list as `a, b and c`.
func andJoin(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// qualifyDefaultExpr rewrites a lowered default expression — of a struct field
// or of a function or method parameter — so the names in it resolve from the
// package doing the CALLING or CONSTRUCTING, not the one that declared the
// default.
//
// A default is source text on the declaring package's metadata, and it is
// transformed at the use site — so it is resolved in the caller's scope. For a
// same-package use that is correct. Across packages it is
// not: `struct Snap(Entries Array[Entry] = EmptyArray[Entry]())` declared in
// `lib` lowers at a call site in `main` as `EmptyArray[Entry]()`, and `Entry`
// names nothing there. The generated Go then fails with `undefined: Entry`,
// pointing at a type the author never wrote at that call site.
//
// Bare identifiers that the declaring package declares — types, functions,
// vals and vars — are qualified with this file's name for that package
// (importing it when this file does not). Anything already qualified, any std
// type (those lower to a selector, not an ident) and any Go builtin is left
// alone — as are the package's names when it is dot-imported and they are
// already in scope. A qualifier of the declaring file's own imports
// (`strings` in `strings.Repeat("=", 2)`) is replaced by this file's name for
// that import, which is added when this file lacks it (see
// transpiler.DefaultScope).
//
// A default from another file of this package names the package's own
// declarations as written; only what it borrows from its file's imports (its
// import qualifiers and dot-imported names) is respelled.
func (t *galaASTTransformer) qualifyDefaultExpr(expr ast.Expr, src defaultSource) (ast.Expr, error) {
	owningPkg := src.pkg
	if expr == nil || owningPkg == "" {
		return expr, nil
	}
	samePkg := owningPkg == t.packageName
	if samePkg && !t.lexicallyScoped(src) {
		return expr, nil
	}
	scope := src.Scope
	if scope == nil {
		scope = &transpiler.DefaultScope{}
	}
	// A dot import puts the declaring package's names in scope unqualified, so
	// they resolve here as written and qualifying would invent an identifier
	// (`struct_defaults_lib.Entry`) that the file never binds.
	var dotted bool
	var vals map[string]*transpiler.PackageValMetadata
	if !samePkg {
		dotted = t.importManager.IsDotImported(owningPkg)
		vals = t.declaringPackageVals(src)
	}

	// An unexported name cannot be reached from another package at all, so
	// qualifying it would only trade one Go error for another. Say what is
	// actually wrong. The error carries no position; transformDefaultExpr
	// places it at the default.
	var err error
	unexported := func(name string) {
		err = galaerr.NewSemanticErrorAt(0, 0, fmt.Sprintf(
			"default expression refers to %q, which is unexported in package %q — "+
				"a default is evaluated at each call or construction site, so everything it names "+
				"must be visible there; export it or use a literal default",
			name, owningPkg))
	}
	qualified := map[ast.Node]bool{}
	rewriteIdent := func(id *ast.Ident) ast.Expr {
		if samePkg {
			return t.qualifyDotImported(id, scope, qualified)
		}
		_, isVal := vals[id.Name]
		private := slices.Contains(scope.PrivateVals, id.Name)
		if !isVal && !private && !t.packageDeclares(owningPkg, id.Name) {
			return t.qualifyDotImported(id, scope, qualified)
		}
		if private || !ast.IsExported(id.Name) {
			unexported(id.Name)
			return id
		}
		if dotted {
			return id
		}
		sel := &ast.SelectorExpr{X: ast.NewIdent(t.importQualifier(scope.PkgPath, owningPkg)), Sel: ast.NewIdent(id.Name)}
		qualified[sel] = true
		return sel
	}
	// Names a lambda default binds itself (its parameters and locals) refer to
	// those bindings, not to the declaring package, whatever they are called.
	bound := boundNames(expr)
	holder := &ast.ParenExpr{X: expr}
	ast.Inspect(holder, func(n ast.Node) bool {
		if qualified[n] {
			return false // rewritten just now; its `pkg` ident is not a reference to qualify
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if base, ok := sel.X.(*ast.Ident); ok && !bound[base.Name] {
				// Already qualified. The one thing to check is that what it
				// names is reachable: a default that calls the declaring
				// package's own unexported helper cannot be evaluated at a
				// use site in another package, and emitting `lib.helper()`
				// would just hand the author a Go visibility error about code
				// they never wrote.
				if !samePkg && base.Name == owningPkg && !ast.IsExported(sel.Sel.Name) {
					unexported(sel.Sel.Name)
				}
				// A qualifier of one of the declaring file's imports.
				if path, ok := scope.Imports[base.Name]; ok {
					sel.X = ast.NewIdent(t.importQualifier(path, base.Name))
					return true
				}
			}
		}
		for _, slot := range referenceSlots(n) {
			if id, ok := (*slot).(*ast.Ident); ok && !bound[id.Name] {
				*slot = rewriteIdent(id)
			}
		}
		return true
	})
	return holder.X, err
}

// qualifyDotImported qualifies id, a bare name of a lowered default from
// another file, when it is a type, function, val or var of a package the
// default's declaring file dot-imports (`EmptyArray` of collection_immutable,
// `ToUpper` of Go's strings) and this file does not dot-import that package
// too. Otherwise id is returned as is. A qualified selector is recorded in
// qualified.
func (t *galaASTTransformer) qualifyDotImported(id *ast.Ident, scope *transpiler.DefaultScope, qualified map[ast.Node]bool) ast.Expr {
	for _, path := range slices.Sorted(maps.Keys(scope.DotImports)) { // deterministic output
		pkgName := scope.DotImports[path]
		if !t.importDeclares(path, pkgName, id.Name) {
			continue
		}
		if t.importManager.IsDotImportedPath(path) {
			return id
		}
		sel := &ast.SelectorExpr{X: ast.NewIdent(t.importQualifier(path, pkgName)), Sel: ast.NewIdent(id.Name)}
		qualified[sel] = true
		return sel
	}
	return id
}

// importDeclares reports whether the package at path, named pkgName, declares
// a type, function, val or var called name: a GALA package, or a Go one
// (`ToUpper` of `strings`), found by its path in the Go type info.
func (t *galaASTTransformer) importDeclares(path, pkgName, name string) bool {
	_, isVal := t.importedVals(path)[name]
	return isVal || t.packageDeclares(pkgName, name) || t.goTypeInfo.HasQualified(path+"."+name)
}

// importQualifier is this file's name for the package at path, named pkgName
// where the default was written: the qualifier of this file's import of it, or
// of one recorded for generated code, else a free name based on pkgName, which
// is then imported. With path unknown it is this file's name for the package
// named pkgName, or pkgName itself.
func (t *galaASTTransformer) importQualifier(path, pkgName string) string {
	if path == "" {
		if q, ok := t.packageQualifier(pkgName); ok {
			return q // an alias, or a keyed package's own name
		}
		return pkgName
	}
	if entry, ok := t.importManager.GetByPath(path); ok && !entry.IsDot {
		q := entry.QualifierFor(entry.PkgName)
		if entry.Implicit() {
			t.importManager.AddTransitive(path, q)
		}
		return q
	}
	q := t.importManager.TransitiveQualifier(path, pkgName)
	t.importManager.AddTransitive(path, q)
	return q
}

// referenceSlots returns the fields of n that hold a reference — a name looked
// up in scope — as opposed to a name being declared (parameter and variable
// names, `:=` targets), a field or method selected from a value (`x.Sel`), or
// a struct-literal key. A lambda default lowers to a function literal, so
// statements and parameter types are covered as well as expressions.
func referenceSlots(n ast.Node) []*ast.Expr {
	var slots []*ast.Expr
	each := func(list []ast.Expr) {
		for i := range list {
			slots = append(slots, &list[i])
		}
	}
	switch n := n.(type) {
	case *ast.ParenExpr:
		slots = append(slots, &n.X)
	case *ast.CallExpr:
		slots = append(slots, &n.Fun)
		each(n.Args)
	case *ast.IndexExpr:
		slots = append(slots, &n.X, &n.Index)
	case *ast.IndexListExpr:
		slots = append(slots, &n.X)
		each(n.Indices)
	case *ast.SelectorExpr:
		slots = append(slots, &n.X)
	case *ast.StarExpr:
		slots = append(slots, &n.X)
	case *ast.UnaryExpr:
		slots = append(slots, &n.X)
	case *ast.BinaryExpr:
		slots = append(slots, &n.X, &n.Y)
	case *ast.CompositeLit:
		slots = append(slots, &n.Type)
		for i, elt := range n.Elts {
			if _, isKV := elt.(*ast.KeyValueExpr); !isKV {
				slots = append(slots, &n.Elts[i])
			}
		}
	case *ast.KeyValueExpr:
		slots = append(slots, &n.Value)
	case *ast.TypeAssertExpr:
		slots = append(slots, &n.X, &n.Type)
	case *ast.ArrayType:
		slots = append(slots, &n.Elt)
	case *ast.MapType:
		slots = append(slots, &n.Key, &n.Value)
	case *ast.Ellipsis:
		slots = append(slots, &n.Elt)
	case *ast.Field:
		slots = append(slots, &n.Type)
	case *ast.ValueSpec:
		slots = append(slots, &n.Type)
		each(n.Values)
	case *ast.ExprStmt:
		slots = append(slots, &n.X)
	case *ast.ReturnStmt:
		each(n.Results)
	case *ast.AssignStmt:
		if n.Tok != token.DEFINE {
			each(n.Lhs)
		}
		each(n.Rhs)
	case *ast.IncDecStmt:
		slots = append(slots, &n.X)
	case *ast.IfStmt:
		slots = append(slots, &n.Cond)
	case *ast.SwitchStmt:
		slots = append(slots, &n.Tag)
	case *ast.CaseClause:
		each(n.List)
	case *ast.RangeStmt:
		slots = append(slots, &n.X)
	case *ast.ForStmt:
		slots = append(slots, &n.Cond)
	}
	return slots
}

// boundNames collects every name a lowered expression declares for itself:
// function-literal parameters and results, `var` names, `:=` targets and range
// variables. Scoping is ignored — a name bound anywhere in the expression is
// never qualified — which errs toward leaving a name as written.
func boundNames(expr ast.Expr) map[string]bool {
	bound := map[string]bool{}
	addIdents := func(list ...ast.Expr) {
		for _, e := range list {
			if id, ok := e.(*ast.Ident); ok {
				bound[id.Name] = true
			}
		}
	}
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			for _, name := range n.Names {
				bound[name.Name] = true
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				bound[name.Name] = true
			}
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				addIdents(n.Lhs...)
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				addIdents(n.Key, n.Value)
			}
		}
		return true
	})
	return bound
}

// packageDeclares reports whether a package declares a type or function of this
// name, using the same "pkg.Name" keys the metadata tables are built with.
func (t *galaASTTransformer) packageDeclares(pkg, name string) bool {
	qualified := pkg + "." + name
	if _, ok := t.structFields[qualified]; ok {
		return true
	}
	if t.typeMetas[qualified] != nil {
		return true
	}
	return t.functions[qualified] != nil
}

// orderedTypeArgs lists the type arguments bound in byName in the order of
// typeParams, nil when one is unbound or unresolved.
func orderedTypeArgs(typeParams []string, byName map[string]transpiler.Type) []transpiler.Type {
	if len(typeParams) == 0 {
		return nil
	}
	out := make([]transpiler.Type, len(typeParams))
	for i, tp := range typeParams {
		typ, ok := byName[tp]
		if !ok || transpiler.IsUnusable(typ) {
			return nil
		}
		out[i] = typ
	}
	return out
}

// fieldHasNoDefault names the field a GALA-E0075 err is about: the value of
// fieldName that a construction of typeName leaves out.
func fieldHasNoDefault(err error, fieldName, typeName string) error {
	var se *galaerr.SemanticError
	if !errors.As(err, &se) || se.Code != galaerr.CodeNoDefaultValue {
		return err
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeNoDefaultValue, se.Line, se.Column,
		fmt.Sprintf("field %q of %q is left out, and %s", fieldName, typeName, se.Msg),
		fmt.Sprintf("give %s a value", fieldName))
}

// isZeroLiteralOf reports whether a default's source text is the literal Go's
// zero value of fieldType, a basic type, already equals, so the default needs
// no element. A default on any other type is lowered as written: it may not
// be that type's zero value (`0` for an `any` field), or not of the type at
// all, which lowering reports.
func isZeroLiteralOf(text string, fieldType transpiler.Type) bool {
	basic, ok := fieldType.(transpiler.BasicType)
	if !ok {
		return false
	}
	text = strings.TrimSpace(text)
	switch basic.Name {
	case "bool":
		return text == "false"
	case "string":
		return text == `""` || text == "``"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
		return text == "0"
	case "float32", "float64":
		return text == "0" || text == "0.0"
	}
	return false
}

