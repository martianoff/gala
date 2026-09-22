package transformer

import (
	"fmt"
	"go/ast"
	"sort"

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
// provided answers whether the call site gave a value for a field; the named
// and positional paths compute it differently, so they pass their own.
// Ordering follows the declaration order in fields, which keeps the emitted
// literal stable and readable.
func (t *galaASTTransformer) fillOmittedStructFields(
	typeName, resolvedTypeName string,
	fields []string,
	provided func(fieldName string) bool,
	immutFlags []bool,
	fieldTypes map[string]transpiler.Type,
	typeArgSubst map[string]ast.Expr,
	line, col int,
) ([]ast.Expr, error) {
	defaults, isShorthand := t.structFieldDefaults(resolvedTypeName)

	var missing []string
	var elts []ast.Expr
	for i, fieldName := range fields {
		if provided(fieldName) {
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

	hint := fmt.Sprintf("pass %s, or give the %s a default in the declaration (e.g. %s int = 0)",
		quoteJoin(missing), label, missing[0])
	if len(defaults) == 0 && len(missing) == len(fields) {
		// Every field is missing and none is defaulted — most likely the caller
		// meant a Go-style literal, which is allowed to be partial.
		hint += fmt.Sprintf("; a partial literal is written %s{...}", typeName)
	}

	return galaerr.NewCodedSemanticError(
		galaerr.CodeMissingStructField,
		line, col,
		fmt.Sprintf("missing required %s %s in construction of %q", label, quoteJoin(missing), typeName),
		hint,
	)
}

// quoteJoin renders a field-name list as `"a", "b" and "c"`.
func quoteJoin(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("%q", names[0])
	}
	out := ""
	for i, n := range names {
		switch {
		case i == 0:
			out = fmt.Sprintf("%q", n)
		case i == len(names)-1:
			out += fmt.Sprintf(" and %q", n)
		default:
			out += fmt.Sprintf(", %q", n)
		}
	}
	return out
}
