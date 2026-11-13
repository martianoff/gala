package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

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

// typeParamValue reports whether the bare name, read as a value, names a type
// parameter of the enclosing declaration: one is bound, and no local binding
// shadows it (see bindingScope).
func (t *galaASTTransformer) typeParamValue(name string) bool {
	return t.activeTypeParams[name] && t.bindingScope(name) == nil
}

// checkTypeParamConversion reports a call of name, a type parameter of the
// enclosing declaration, that is no conversion. A conversion takes exactly one
// value, by position, and a lambda has no type to convert from: such a call
// meant a function the type parameter shadows, as `Try(() => 1)` inside
// `func g[Try any]` does.
func (t *galaASTTransformer) checkTypeParamConversion(name string, c callSrc) error {
	if args := c.args.list; len(args) == 1 && !args[0].named() && !args[0].lambda() {
		return nil
	}
	return t.typeParamMisuseErrorAt(c.at(), name, "cannot be called: a conversion to it takes exactly one value, not a lambda")
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
// A parameter declared with a constraint (`T comparable`, `C Iterable[T, C]`)
// records it in typeParamConstraints; the constraint is read once every name
// of the list is bound, since it may name any of them.
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
	for _, field := range params {
		if field.Type == nil {
			continue
		}
		for _, n := range field.Names {
			if slices.Contains(added, n.Name) {
				t.constrainTypeParam(n.Name, t.astTypeToTranspilerType(field.Type))
			}
		}
	}
	return func() {
		for _, name := range added {
			delete(t.activeTypeParams, name)
			delete(t.typeParamConstraints, name)
		}
	}
}

// constrainReceiverTypeParams records the constraints of the type parameters
// a receiver type declares (`func (b Box[U]) ...`): those meta, the receiver
// type, declares at the same positions. A receiver repeats no constraint, so
// bindTypeParams has none for them. A constraint naming the type's parameters
// (`C Iterable[T, C]`) names the receiver's instead.
func (t *galaASTTransformer) constrainReceiverTypeParams(params []*ast.Field, meta *transpiler.TypeMetadata) {
	if meta == nil || len(meta.TypeParamConstraints) == 0 {
		return
	}
	n := min(len(params), len(meta.TypeParams))
	renames := make(map[string]transpiler.Type, n)
	for i := range n {
		if len(params[i].Names) == 1 && params[i].Names[0].Name != "_" {
			renames[meta.TypeParams[i]] = transpiler.BasicType{Name: params[i].Names[0].Name}
		}
	}
	for i := range n {
		if c := meta.TypeParamConstraints[meta.TypeParams[i]]; c != nil {
			for _, n := range params[i].Names {
				t.constrainTypeParam(n.Name, transpiler.SubstituteTypeParams(t.inPackage(c, meta), renames))
			}
		}
	}
}

// constrainTypeParam records constraint as the active type parameter name's.
func (t *galaASTTransformer) constrainTypeParam(name string, constraint transpiler.Type) {
	if t.typeParamConstraints == nil {
		t.typeParamConstraints = make(map[string]transpiler.Type)
	}
	t.typeParamConstraints[name] = constraint
}

// typeParamConstraint is the constraint of the type parameter typ names, when
// typ names one of the enclosing declaration that a named interface
// constrains (`C Iterable[T, C]`, `T Shower`): the type whose methods a value
// of typ has. ok is false for any other type, a parameter constrained by
// `any` or `comparable` included.
func (t *galaASTTransformer) typeParamConstraint(typ transpiler.Type) (transpiler.Type, bool) {
	_, c, ok := t.constrainedTypeParam(typ)
	if !ok || !transpiler.IsInterfaceConstraint(c) {
		return nil, false
	}
	return c, true
}

// constrainedTypeParam returns the name of the type parameter of the
// enclosing declaration typ names, and its recorded constraint. ok is false
// when typ names no such parameter, or one with no recorded constraint.
func (t *galaASTTransformer) constrainedTypeParam(typ transpiler.Type) (name string, constraint transpiler.Type, ok bool) {
	switch v := typ.(type) {
	case transpiler.BasicType:
		name = v.Name
	case transpiler.NamedType:
		if v.Package != "" {
			return "", nil, false
		}
		name = v.Name
	default:
		return "", nil, false
	}
	if !t.activeTypeParams[name] {
		return "", nil, false
	}
	constraint, ok = t.typeParamConstraints[name]
	return name, constraint, ok
}

// typeParamMisuseErrorAt reports name, a type parameter of the enclosing
// declaration, used at a node where it cannot stand — a name
// it shadows was meant.
func (t *galaASTTransformer) typeParamMisuseErrorAt(at srcNode, name, what string) error {
	err := t.semanticErrorAtNode(at, fmt.Sprintf("'%s' is a type parameter of the enclosing declaration and %s", name, what))
	err.Hint = fmt.Sprintf("a type parameter shadows every other '%s' in its declaration; rename the type parameter to reach the outer one", name)
	return err
}

// onlyTypeParams binds exactly names, and none of the declaration being
// transformed, for code read in the scope of another declaration — a callee's
// signature, a parameter's default — and returns the function that restores
// the enclosing set.
func (t *galaASTTransformer) onlyTypeParams(names []string) func() {
	enclosing, constraints := t.activeTypeParams, t.typeParamConstraints
	t.activeTypeParams = make(map[string]bool, len(names))
	t.typeParamConstraints = map[string]transpiler.Type{}
	for _, name := range names {
		t.activeTypeParams[name] = true
	}
	return func() { t.activeTypeParams, t.typeParamConstraints = enclosing, constraints }
}

// nameWildcardReceiverTypeArgs gives each type argument written `_` in src,
// a receiver type (`Holder[_, C]`), a name of its own under the reserved
// prefix in recvType, its lowering (`Holder[_gala_wild_T, C]`, T being meta's
// parameter at that position), which no source name has. Another
// parameter's constraint can name the parameter `_` stands for
// (`C Seq[T, C]`): the receiver's constraints, and those of the function a
// generic method lowers to, must then be able to name it too. It returns the
// type parameters of the renamed receiver, in order, so that each lines up
// with meta's parameter at its position; nil, renaming nothing, when src
// writes no `_`.
func nameWildcardReceiverTypeArgs(src typeSrc, recvType ast.Expr, meta *transpiler.TypeMetadata) []*ast.Field {
	if star, ok := recvType.(*ast.StarExpr); ok {
		recvType = star.X
	}
	_, args := splitCallFunTypeArgs(recvType)
	written := receiverTypeArgTexts(src)
	if meta == nil || len(args) != len(meta.TypeParams) || len(written) != len(args) || !slices.Contains(written, "_") {
		return nil
	}
	params := make([]*ast.Field, 0, len(args))
	for _, arg := range args {
		if _, ok := arg.(*ast.Ident); !ok {
			return nil
		}
	}
	for i, arg := range args {
		// `_` is lowered as Go's wildcard type, `any`.
		id := arg.(*ast.Ident)
		if written[i] == "_" {
			id.Name = transpiler.WildcardTypeParamPrefix + meta.TypeParams[i]
		}
		params = append(params, &ast.Field{Names: []*ast.Ident{ast.NewIdent(id.Name)}})
	}
	return params
}

// receiverTypeParams is the type parameters a receiver type declares: the
// type arguments of `Box[T]` or `*Pair[A, B]`, each a bare name.
func receiverTypeParams(s typeSrc) []*ast.Field {
	var params []*ast.Field
	for _, name := range receiverTypeParamNamesOf(s) {
		params = append(params, &ast.Field{Names: []*ast.Ident{ast.NewIdent(name)}})
	}
	return params
}

// receiverTypeParamNamesOf is the names of receiverTypeParams of s.
func receiverTypeParamNamesOf(s typeSrc) []string {
	var names []string
	for _, name := range receiverTypeArgTexts(s) {
		// `_` is the wildcard type, not a parameter name.
		if name != "_" && token.IsIdentifier(name) {
			names = append(names, name)
		}
	}
	return names
}

// receiverTypeArgTexts is the source text of each type argument of s, a
// receiver type (`Box[T]`, `*Pair[_, B]`), in order.
func receiverTypeArgTexts(s typeSrc) []string {
	s, _ = s.pointerElem() // *Box[T], Ptr[Box[T]]
	for s.ok() && !s.named() {
		elems := s.elems()
		if len(elems) != 1 {
			break
		}
		s = elems[0]
	}
	if !s.ok() {
		return nil
	}
	args, _ := s.typeArgs()
	texts := make([]string, len(args))
	for i, arg := range args {
		texts[i] = arg.text()
	}
	return texts
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
