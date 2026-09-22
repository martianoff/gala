package transformer

import (
	"fmt"
	"go/ast"
	"sort"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
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

// structFieldDefaults returns the field-name → default-expression-text map for
// a struct, or nil when the type declares no defaults. resolvedTypeName is the
// key already resolved by resolveStructTypeName; getTypeMeta re-resolves it
// against the metadata tables and falls back to the RichAST for types added
// after the initial copy.
func (t *galaASTTransformer) structFieldDefaults(resolvedTypeName string) (defaults map[string]string, isShorthand bool) {
	meta := t.getTypeMeta(resolvedTypeName)
	if meta == nil {
		return nil, false
	}
	return meta.FieldDefaults, meta.IsShorthand
}

// fillOmittedStructFields returns the extra KeyValueExprs a constructor call
// needs for the fields it did not supply, and reports the first required field
// it left out.
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
) ([]ast.Expr, error) {
	defaults, isShorthand := t.structFieldDefaults(resolvedTypeName)
	immutFlags := t.structImmutFields[resolvedTypeName]
	fieldTypes := t.structFieldTypes[resolvedTypeName]

	var missing []string
	var elts []ast.Expr
	for i, fieldName := range fields {
		if provided(i, fieldName) {
			continue
		}
		defaultText, hasDefault := defaults[fieldName]
		if !hasDefault {
			missing = append(missing, fieldName)
			continue
		}
		// Re-parsed and re-transformed per construction site, so a default like
		// `time.Now()` is evaluated at each construction rather than once at
		// declaration — the same contract function parameter defaults have.
		val, err := t.transformDefaultExpr(defaultText)
		if err != nil {
			return nil, err
		}
		// The expression was resolved in THIS package's scope; names it borrows
		// from the declaring package need qualifying. See qualifyDefaultExpr.
		val, err = t.qualifyDefaultExpr(val, t.declaringPackageOf(resolvedTypeName))
		if err != nil {
			return nil, err
		}
		if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
			val = t.wrapImmutableFieldValue(val, fieldTypes[fieldName], typeArgSubst)
		}
		elts = append(elts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: val})
	}

	// Only the shorthand form can declare a field optional, so only it can
	// hold a call site to supplying the rest.
	if len(missing) > 0 && isShorthand {
		return nil, missingStructFieldsError(typeName, missing, defaults, fields, line, col)
	}
	return elts, nil
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
	return galaerr.NewCodedSemanticError(
		galaerr.CodeMissingStructField,
		line, col,
		fmt.Sprintf("unknown %s %s in construction of %q", label, quoteJoin(unknown), typeName),
		fmt.Sprintf("%s declares: %s", typeName, strings.Join(fields, ", ")),
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
	defaults map[string]string,
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
	if len(q) < 2 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

// checkUnknownStructFields reports named arguments that match no field of the
// struct. Shorthand-only, for the same reason the required-field check is: a
// block-form struct is a Go-shaped layout, and its construction is checked by
// the Go compiler against the real field set.
func (t *galaASTTransformer) checkUnknownStructFields(
	typeName, resolvedTypeName string,
	fields []string,
	namedArgs map[string]ast.Expr,
	line, col int,
) error {
	if _, isShorthand := t.structFieldDefaults(resolvedTypeName); !isShorthand {
		return nil
	}
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f] = true
	}
	var unknown []string
	for name := range namedArgs {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return unknownStructFieldError(typeName, unknown, fields, line, col)
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

// qualifyDefaultExpr rewrites a lowered default expression so the names in it
// resolve from the package doing the CONSTRUCTING, not the one that declared
// the default.
//
// A default is source text on the declaring package's metadata, and it is
// transformed at the construction site — so it is resolved in the caller's
// scope. For a same-package construction that is correct. Across packages it is
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

	var err error
	var walk func(ast.Expr) ast.Expr
	rewriteIdent := func(id *ast.Ident) ast.Expr {
		if id == nil || !t.packageDeclares(owningPkg, id.Name) {
			return id
		}
		// An unexported name cannot be reached from another package at all, so
		// qualifying it would only trade one Go error for another. Say what is
		// actually wrong.
		if !ast.IsExported(id.Name) {
			err = galaerr.NewSemanticErrorAt(0, 0, fmt.Sprintf(
				"default expression for a field of %q refers to %q, which is unexported in package %q — "+
					"a default is evaluated at each construction site, so everything it names must be visible there; "+
					"export it or use a literal default",
				owningPkg, id.Name, owningPkg))
			return id
		}
		return &ast.SelectorExpr{X: ast.NewIdent(owningPkg), Sel: ast.NewIdent(id.Name)}
	}
	walk = func(e ast.Expr) ast.Expr {
		switch n := e.(type) {
		case nil:
			return nil
		case *ast.Ident:
			return rewriteIdent(n)
		case *ast.CallExpr:
			n.Fun = walk(n.Fun)
			for i := range n.Args {
				n.Args[i] = walk(n.Args[i])
			}
		case *ast.IndexExpr:
			n.X = walk(n.X)
			n.Index = walk(n.Index)
		case *ast.IndexListExpr:
			n.X = walk(n.X)
			for i := range n.Indices {
				n.Indices[i] = walk(n.Indices[i])
			}
		case *ast.SelectorExpr:
			// Already qualified. The one thing to check is that what it names
			// is reachable: a default that calls the declaring package's own
			// unexported helper cannot be evaluated at a call site in another
			// package, and emitting `lib.helper()` would just hand the author
			// a Go visibility error about code they never wrote.
			if base, ok := n.X.(*ast.Ident); ok && base.Name == owningPkg && !ast.IsExported(n.Sel.Name) {
				err = galaerr.NewSemanticErrorAt(0, 0, fmt.Sprintf(
					"default expression refers to %q, which is unexported in package %q — "+
						"a default is evaluated at each construction site, so everything it names "+
						"must be visible there; export it or use a literal default",
					n.Sel.Name, owningPkg))
			}
		case *ast.StarExpr:
			n.X = walk(n.X)
		case *ast.UnaryExpr:
			n.X = walk(n.X)
		case *ast.BinaryExpr:
			n.X = walk(n.X)
			n.Y = walk(n.Y)
		case *ast.ParenExpr:
			n.X = walk(n.X)
		case *ast.CompositeLit:
			n.Type = walk(n.Type)
			for i := range n.Elts {
				n.Elts[i] = walk(n.Elts[i])
			}
		case *ast.KeyValueExpr:
			// The key of a struct literal is a field name, not a reference.
			n.Value = walk(n.Value)
		}
		return e
	}

	out := walk(expr)
	return out, err
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
	if fns, ok := t.functions[pkg]; ok && fns != nil && fns.Name == name {
		return true
	}
	return false
}

// declaringPackageOf returns the package a type was declared in, or "" when
// that is unknown.
func (t *galaASTTransformer) declaringPackageOf(resolvedTypeName string) string {
	if meta := t.getTypeMeta(resolvedTypeName); meta != nil {
		return meta.Package
	}
	return ""
}
