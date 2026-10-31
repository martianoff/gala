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
// its name ahead of a package-level type of the same name, as Go's scoping
// says it must. (A package-level type is keyed `pkg.Name`, except in package
// main, where its key is the bare name too: scopeLocalTypeName saves and
// restores it, so the shadowing holds there as well.)
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
	decls, err := t.lowerTypeDecl(readTypeDecl(ctx), true)
	if err != nil || len(decls) == 0 {
		return nil, err
	}
	return decls[0], nil
}

// declareLocalType scopes d's name to the current block and, for a struct,
// registers its TypeMetadata. It runs before the fields are lowered, so a
// field naming the type itself (`next Option[Node]`) resolves to it; the
// field layout is filled in once they are (see lowerTypeDecl).
func (t *galaASTTransformer) declareLocalType(d typeDecl) *transpiler.TypeMetadata {
	t.scopeLocalTypeName(d.name)
	if d.kind != structTypeDecl {
		return nil
	}
	meta := &transpiler.TypeMetadata{
		Name:    d.name,
		Package: t.packageName,
		Methods: map[string]*transpiler.MethodMetadata{},
	}
	for _, p := range d.typeParams {
		meta.TypeParams = append(meta.TypeParams, p.name)
		if p.constraint != "" {
			if meta.TypeParamConstraints == nil {
				meta.TypeParamConstraints = map[string]string{}
			}
			meta.TypeParamConstraints[p.name] = p.constraint
		}
	}
	t.typeMetas[d.name] = meta
	return meta
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
