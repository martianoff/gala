package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// A type name means a type parameter in two situations, and neither is decided
// by how the name is spelled:
//
//   - It is bound by an enclosing generic declaration — the function, method,
//     struct or sealed type being transformed declares it (activeTypeParams).
//     Such a binder shadows any same-named type outside it, exactly as in Go,
//     so `func first[A any](xs Array[A]) A` means its own A even when the
//     package also declares `type A struct{...}`.
//   - It is a placeholder left in a callee's signature: inference could not
//     bind the callee's own parameter, so its declared name (Option's T,
//     Either's B) surfaced in a caller that never declared it. A name counts as
//     such a placeholder only when some known generic declaration declares a
//     type parameter by that name AND no type by that name is visible here.
//
// Everything else is a type. In particular a user type called `A`, `T` or `V`,
// or a Go type such as `testing.B`, is an ordinary concrete type outside a
// generic declaration that rebinds the name.

// isActiveTypeParam reports whether name, as it appears in the code being
// transformed, denotes a type parameter rather than a concrete type. name may be
// package-qualified ("std.T"), which is how a callee's placeholder often
// arrives; a qualified name that resolves to a declared type ("testing.B") is
// that type.
func (t *galaASTTransformer) isActiveTypeParam(name string) bool {
	qualifier, bare := splitPackageQualifier(name)
	if !token.IsIdentifier(bare) || (qualifier != "" && !token.IsIdentifier(qualifier)) {
		// "*pkg.T", "Array[T]", "func(T) U": composite type strings are never
		// a type parameter themselves.
		return false
	}
	if qualifier == "" && t.activeTypeParams[bare] {
		return true
	}
	if !t.activeTypeParams[bare] && !t.declaredTypeParamNames()[bare] {
		return false
	}
	return !t.declaresType(qualifier, bare)
}

// isUnboundTypeParam reports whether name is a type parameter that the code
// being transformed does not bind — a callee's placeholder that inference left
// behind. Unlike a parameter of the enclosing declaration, it is not a type the
// generated Go can name here.
func (t *galaASTTransformer) isUnboundTypeParam(name string) bool {
	if t.activeTypeParams[name] {
		return false
	}
	return t.isActiveTypeParam(name)
}

// bindTypeParams brings the type parameters a generic declaration declares into
// scope for the rest of that declaration and returns the function that takes
// them back out. A name the enclosing scope already bound stays bound on exit.
func (t *galaASTTransformer) bindTypeParams(params ...*ast.Field) func() {
	var added []string
	for _, field := range params {
		for _, n := range field.Names {
			if !t.activeTypeParams[n.Name] {
				t.activeTypeParams[n.Name] = true
				added = append(added, n.Name)
			}
		}
	}
	return func() {
		for _, name := range added {
			delete(t.activeTypeParams, name)
		}
	}
}

// typeParamMisuseError reports name, a type parameter of the enclosing
// declaration, used where it cannot stand — a name it shadows was meant.
func (t *galaASTTransformer) typeParamMisuseError(ctx antlr.ParserRuleContext, name, what string) error {
	err := t.semanticErrorAt(ctx, fmt.Sprintf("'%s' is a type parameter of the enclosing declaration and %s", name, what))
	err.Hint = fmt.Sprintf("a type parameter shadows every other '%s' in its declaration; rename the type parameter to reach the outer one", name)
	return err
}

// onlyTypeParams binds exactly names, and none of the declaration being
// transformed, for code read in the scope of another declaration — a callee's
// signature, a parameter's default — and returns the function that restores
// the enclosing set.
func (t *galaASTTransformer) onlyTypeParams(names []string) func() {
	enclosing := t.activeTypeParams
	t.activeTypeParams = make(map[string]bool, len(names))
	for _, name := range names {
		t.activeTypeParams[name] = true
	}
	return func() { t.activeTypeParams = enclosing }
}

// receiverTypeParams is the type parameters a receiver type declares: the
// type arguments of `Box[T]` or `*Pair[A, B]`, each a bare name.
func receiverTypeParams(ctx grammar.ITypeContext) []*ast.Field {
	for ctx != nil && ctx.QualifiedIdentifier() == nil && len(ctx.AllType_()) == 1 {
		ctx = ctx.Type_(0) // *Box[T]
	}
	if ctx == nil || ctx.TypeArguments() == nil {
		return nil
	}
	// A partial parse (an editor's `Box[]`) has no type list.
	list, ok := ctx.TypeArguments().(*grammar.TypeArgumentsContext).TypeList().(*grammar.TypeListContext)
	if !ok {
		return nil
	}
	var params []*ast.Field
	for _, arg := range list.AllType_() {
		// `_` is the wildcard type, not a parameter name.
		if name := arg.GetText(); name != "_" && token.IsIdentifier(name) {
			params = append(params, &ast.Field{Names: []*ast.Ident{ast.NewIdent(name)}})
		}
	}
	return params
}

// fieldListOrNil returns the fields of a possibly-nil field list.
func fieldListOrNil(list *ast.FieldList) []*ast.Field {
	if list == nil {
		return nil
	}
	return list.List
}

// declaredTypeParamNames is the set of names some known generic declaration —
// a GALA function, type or method, or a Go function or type — declares as a type
// parameter. It is built once per file from the metadata the file is
// transformed against.
func (t *galaASTTransformer) declaredTypeParamNames() map[string]bool {
	if t.typeParamNames != nil {
		return t.typeParamNames
	}
	names := make(map[string]bool, 32)
	add := func(params []string) {
		for _, p := range params {
			names[p] = true
		}
	}
	for _, fn := range t.functions {
		add(fn.TypeParams)
	}
	for _, meta := range t.typeMetas {
		add(meta.TypeParams)
		for _, m := range meta.Methods {
			add(m.TypeParams)
		}
	}
	if t.goTypeInfo != nil {
		for _, fn := range t.goTypeInfo.Functions {
			add(fn.TypeParams)
		}
		for _, td := range t.goTypeInfo.Types {
			add(td.TypeParams)
		}
	}
	t.typeParamNames = names
	return names
}

// declaresType reports whether qualifier.bare (or bare alone, when qualifier is
// empty) names a type visible from the file being transformed: a primitive, a
// GALA type or alias, or a Go type of an imported package.
func (t *galaASTTransformer) declaresType(qualifier, bare string) bool {
	if qualifier == t.packageName {
		// This package's own types are keyed by their bare name.
		qualifier = ""
	}
	name := bare
	if qualifier != "" {
		name = qualifier + "." + bare
	} else {
		if transpiler.IsPrimitiveType(bare) {
			return true
		}
		if _, ok := t.typeAliases[bare]; ok {
			return true
		}
	}
	if t.getTypeMeta(name) != nil {
		return true
	}
	if qualifier == "" || t.goTypeInfo == nil {
		return false
	}
	if t.goTypeInfo.DeclaresType(qualifier, bare) {
		return true
	}
	if pkg, ok := t.importManager.ResolveAlias(qualifier); ok && pkg != qualifier {
		return t.goTypeInfo.DeclaresType(pkg, bare)
	}
	return false
}
