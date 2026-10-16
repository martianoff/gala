package transformer

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
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

// isShorthandStruct reports whether a type came from the shorthand form and
// declares at least one field — the shape for which `Cfg()` is a construction
// meaning "all defaults" rather than a Go type conversion.
//
// A zero-field struct is excluded because the Apply/companion paths already
// own that spelling, and a block-form struct because it is a Go-shaped layout
// whose construction Go itself checks.
func (t *galaASTTransformer) isShorthandStruct(resolvedTypeName string) bool {
	meta := t.getTypeMeta(resolvedTypeName)
	return meta != nil && meta.IsShorthand && len(meta.FieldNames) > 0
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
// Only bare identifiers that the declaring package actually declares are
// rewritten. Anything already qualified, any std type (those lower to a
// selector, not an ident) and any Go builtin is left alone — as is everything,
// when the declaring package is dot-imported and its names are already in
// scope.
func (t *galaASTTransformer) qualifyDefaultExpr(expr ast.Expr, owningPkg string) (ast.Expr, error) {
	if expr == nil || owningPkg == "" || owningPkg == t.packageName {
		return expr, nil
	}
	// A dot import puts the declaring package's names in scope unqualified, so
	// they resolve here as written and qualifying would invent an identifier
	// (`struct_defaults_lib.Entry`) that the file never binds.
	for _, dotted := range t.importManager.GetDotImports() {
		if dotted == owningPkg {
			return expr, nil
		}
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
		if !t.packageDeclares(owningPkg, id.Name) {
			return id
		}
		if !ast.IsExported(id.Name) {
			unexported(id.Name)
			return id
		}
		qualifier := owningPkg
		if q, ok := t.packageQualifier(owningPkg); ok {
			qualifier = q // this file's name for the package: an alias, or a keyed package's own name
		}
		sel := &ast.SelectorExpr{X: ast.NewIdent(qualifier), Sel: ast.NewIdent(id.Name)}
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
		// Already qualified. The one thing to check is that what it names is
		// reachable: a default that calls the declaring package's own
		// unexported helper cannot be evaluated at a use site in another
		// package, and emitting `lib.helper()` would just hand the author a Go
		// visibility error about code they never wrote.
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if base, ok := sel.X.(*ast.Ident); ok && base.Name == owningPkg && !ast.IsExported(sel.Sel.Name) {
				unexported(sel.Sel.Name)
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

