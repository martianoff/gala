package transformer

import (
	"go/ast"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// transformLocalTypeDeclaration lowers a type declared inside a function body.
//
// A local struct is constructed like a top-level one — `Inner(1)`,
// `Inner(N = 1)` — so it gets the metadata a top-level struct has from the
// analyzer: its field layout and a TypeMetadata, keyed by its bare name. The
// bare key is an exact match, so inside its block the local type answers for
// its name ahead of a package-level type of the same name (whose key is
// `pkg.Name`), as Go's scoping says it must.
//
// Everything registered here is scoped to the enclosing block: popping the
// block's scope puts back what the declaration replaced, so the type is not
// seen after its block, and a same-named type in another function is a
// declaration of its own.
//
// Go has no methods on a local type, so only the type itself is emitted,
// without the Copy/Equal/Unapply methods a top-level struct gets (copy and
// pattern matching lower inline instead).
func (t *galaASTTransformer) transformLocalTypeDeclaration(ctx *grammar.TypeDeclarationContext) (ast.Decl, error) {
	name := ctx.Identifier().GetText()
	t.scopeLocalTypeName(name)

	var meta *transpiler.TypeMetadata
	if ctx.StructType() != nil {
		// Registered before the fields are lowered, so a field naming the
		// type itself (`next Option[Node]`) resolves to it.
		meta = &transpiler.TypeMetadata{
			Name:    name,
			Package: t.packageName,
			Methods: map[string]*transpiler.MethodMetadata{},
		}
		if tp := ctx.TypeParameters(); tp != nil {
			for _, p := range tp.(*grammar.TypeParametersContext).TypeParameterList().AllTypeParameter() {
				meta.TypeParams = append(meta.TypeParams, p.(*grammar.TypeParameterContext).Identifier(0).GetText())
			}
		}
		t.typeMetas[name] = meta
	}

	decls, err := t.transformTypeDeclaration(ctx, true)
	if err != nil || len(decls) == 0 {
		return nil, err
	}
	if meta != nil {
		meta.Fields = t.structFieldTypes[name]
		meta.FieldNames = t.structFields[name]
		meta.ImmutFlags = t.structImmutFields[name]
	}
	return decls[0], nil
}

// scopeLocalTypeName clears the transformer's bare-name entries for name, and
// has the current scope put them back when it is popped. The declaration then
// registers its own entries over a clean slate: a local struct does not keep
// an outer alias of its name, nor a local alias an outer struct's fields.
func (t *galaASTTransformer) scopeLocalTypeName(name string) {
	if t.currentScope == nil {
		return
	}
	t.currentScope.onPop = append(t.currentScope.onPop,
		saveEntry(t.structFields, name),
		saveEntry(t.structFieldTypes, name),
		saveEntry(t.structImmutFields, name),
		saveEntry(t.typeMetas, name),
		saveEntry(t.typeAliases, name),
	)
}

// saveEntry deletes m[key] and returns a func that puts m[key] back to what it
// was: its old value, or absent.
func saveEntry[V any](m map[string]V, key string) func() {
	old, had := m[key]
	delete(m, key)
	return func() {
		if had {
			m[key] = old
		} else {
			delete(m, key)
		}
	}
}
