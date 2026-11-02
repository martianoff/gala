package transformer

import (
	"fmt"
	"go/ast"
	"iter"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// GALA-E0061: a sealed variant named where a type is expected.
//
// `case Circle(R float64)` in `sealed type Shape` declares a constructor and
// an extractor, not a type: every value it builds is a Shape, and the fields
// live on Shape. In generated Go, Circle is the empty companion struct that
// carries Apply and Unapply, so `func radius(c Circle)` compiled to a
// parameter of that empty struct — `c.R` did not exist and `Circle(2.0)` (a
// Shape) could not be passed. A typed pattern `case c: Circle` was worse: it
// asked whether a Shape is the companion struct, which is never true, so the
// arm silently never matched.
//
// checkVariantTypeNamesNative walks every type the grammar parses as one —
// parameter, result, field, val/var annotation, type argument, typed pattern,
// alias target, composite literal — before the declarations are transformed,
// so every one is checked in source order, including those a transformation
// lowers speculatively and discards. An interpolated expression is parsed
// separately, so parseAndTransformExpr checks it when it parses it.
//
// Explicit type arguments (`ArrayOf[Circle](...)`, `case Unwrap[Circle](v)`)
// parse as expressions, so they are checked where they are lowered:
// resolveIndexAccess, where scope tells an instantiation from an index, and
// transformConstructorCallPattern, where they always are type arguments.

// collectVariantNames returns the bare name of every sealed variant the
// transform can see, so a name that is no variant anywhere costs one lookup.
func collectVariantNames(typeMetas map[string]*transpiler.TypeMetadata) map[string]bool {
	names := make(map[string]bool)
	for _, meta := range typeMetas {
		if !meta.IsSealed {
			continue
		}
		for _, sv := range meta.SealedVariants {
			names[sv.Name] = true
		}
	}
	return names
}

// variantTypeRef is a type written in the source whose name is spelled like a
// sealed variant: name (bare or `qualifier.Name`), its type arguments as
// written, and where it is written.
type variantTypeRef struct {
	name     string
	typeArgs string
	at       srcNode
}

// variantTypeRefs is what checkVariantTypeNamesNative reads of a tree: the names of
// the types it declares and, in source order, the types it writes that are
// spelled like a sealed variant.
//
// A type declared in the file shadows a variant of the same name. Scoping is
// not tracked for types: one declared anywhere in the file (a function may
// declare a local type) is never reported, which can miss a variant but
// cannot reject a valid program. A type parameter is scoped to its
// declaration; see typeParamInScopeNative.
type variantTypeRefs struct {
	declared map[string]bool
	refs     []variantTypeRef
}

// checkVariantTypeNamesNative reports the first type below n that names a
// sealed variant, or nil when there is none.
func (t *galaASTTransformer) checkVariantTypeNamesNative(n native.GalaNodeRef) error {
	if len(t.variantNames) == 0 {
		return nil // nothing to spell like a variant
	}
	r := variantTypeRefs{declared: make(map[string]bool)}
	t.collectVariantTypeRefsNative(n, &r)
	return t.reportVariantTypeRefs(r)
}

// reportVariantTypeRefs reports the first of r's types that names a sealed
// variant, or nil when none does.
func (t *galaASTTransformer) reportVariantTypeRefs(r variantTypeRefs) error {
	for _, ref := range r.refs {
		if r.declared[ref.name] || typeParamInScopeNative(ref.at.node, ref.name) {
			continue
		}
		line, col := ref.at.start()
		if err := t.variantTypeError(ref.name, ref.typeArgs, line, col); err != nil {
			t.nativeShape(ref.at.node, "variant type")
			return err
		}
	}
	return nil
}

// addVariantRef adds the type written as qualifier.name (qualifier "" for a bare name)
// at at to r when name is spelled like a sealed variant; typeArgs is the text
// of its type arguments, read only then.
func (t *galaASTTransformer) addVariantRef(r *variantTypeRefs, qualifier, name string, typeArgs func() string, at srcNode) {
	if !t.variantNames[name] {
		return
	}
	ref := variantTypeRef{name: name, at: at}
	if qualifier != "" {
		ref.name = qualifier + "." + name
	}
	if typeArgs != nil {
		ref.typeArgs = typeArgs()
	}
	r.refs = append(r.refs, ref)
}

// collectVariantTypeRefsNative adds the declarations and the types of n, a
// node of the native tree, and of every node below it to r, in source order,
// as checkVariantTypeNamesNative reads them.
func (t *galaASTTransformer) collectVariantTypeRefsNative(n native.GalaNodeRef, r *variantTypeRefs) {
	switch n.Kind() {
	case native.GalaKIND_typeDeclaration:
		r.declared[native.TypeDeclarationNode{GalaNodeRef: n}.Identifier().GetText()] = true
	case native.GalaKIND_structShorthandDeclaration:
		r.declared[native.StructShorthandDeclarationNode{GalaNodeRef: n}.Identifier().GetText()] = true
	case native.GalaKIND_sealedTypeDeclaration:
		r.declared[native.SealedTypeDeclarationNode{GalaNodeRef: n}.Identifier().GetText()] = true
	case native.GalaKIND_opaqueTypeDeclaration:
		r.declared[native.OpaqueTypeDeclarationNode{GalaNodeRef: n}.Identifier().GetText()] = true
	case native.GalaKIND_type:
		typ := native.TypeNode{GalaNodeRef: n}
		if qid := typ.QualifiedIdentifier(); qid.Ok() {
			// A bare name, or a qualifier and a name: a longer one is no
			// type of this file's or an import's.
			var ids [2]string
			count := 0
			for id := range galaparser.NativeKids(qid.GalaNodeRef, native.GalaKIND_identifier) {
				if count == len(ids) {
					count++
					break
				}
				ids[count] = galaparser.NativeText(id)
				count++
			}
			qualifier, name := "", ids[0]
			if count == 2 {
				qualifier, name = ids[0], ids[1]
			}
			if count <= 2 {
				t.addVariantRef(r, qualifier, name, func() string {
					if args := typ.TypeArguments(); args.Ok() {
						return galaparser.NativeText(args.GalaNodeRef)
					}
					return ""
				}, srcNode{node: qid.GalaNodeRef})
			}
		}
	case native.GalaKIND_parameter:
		// A function type's parameters written without names, as in
		// `func(Circle) int`, parse as names with no type; the function
		// type uses each name as the type.
		p := native.ParameterNode{GalaNodeRef: n}
		if id := p.Identifier(); id.Ok() && !p.Type_().Ok() && t.variantNames[id.GetText()] && isFuncTypeSignatureNative(n) {
			t.addVariantRef(r, "", id.GetText(), nil, srcNode{node: n})
		}
	case native.GalaKIND_typeAlias:
		// `type C Circle` parses as the alias's bare-identifier
		// alternative, which is not a type.
		if id := (native.TypeAliasNode{GalaNodeRef: n}).Identifier(); id.Ok() {
			t.addVariantRef(r, "", id.GetText(), nil, srcNode{node: n})
		}
	}
	for i := range n.ChildCount() {
		if c := n.Child(i); !c.IsToken() {
			t.collectVariantTypeRefsNative(c.Node(), r)
		}
	}
}

// typeParamInScopeNative reports whether a declaration enclosing n declares
// a type parameter called name — in its type parameter list, or, for a
// method, as a type argument of its receiver (`func (b Box[Left]) Get()
// Left`).
func typeParamInScopeNative(n native.GalaNodeRef, name string) bool {
	for ; n.Ok(); n = n.Parent() {
		if n.Kind() == native.GalaKIND_functionDeclaration {
			if recv := (native.FunctionDeclarationNode{GalaNodeRef: n}).Receiver(); recv.Ok() &&
				slices.Contains(receiverTypeParamNamesOf(nativeType(recv.Type_())), name) {
				return true
			}
		}
		params, ok := firstNativeKid(n, native.GalaKIND_typeParameters)
		if !ok {
			continue
		}
		list := native.TypeParametersNode{GalaNodeRef: params}.TypeParameterList()
		for tp := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_typeParameter) {
			if (native.TypeParameterNode{GalaNodeRef: tp}).Identifier(0).GetText() == name {
				return true
			}
		}
	}
	return false
}

// isFuncTypeSignatureNative reports whether p, a parameter node, belongs to
// the signature of a function type (`func(...)` in a type position) rather
// than of a function, method or lambda, whose untyped parameters are names.
func isFuncTypeSignatureNative(p native.GalaNodeRef) bool {
	// parameter → parameterList → parameters → signature → type
	n := p
	for range 4 {
		if n = n.Parent(); !n.Ok() {
			return false
		}
	}
	return n.Kind() == native.GalaKIND_type
}

// checkVariantTypeArgs reports a sealed variant written as an explicit type
// argument of base, as in `ArrayOf[Circle](...)`. The grammar reads type
// arguments as an index, so a variant name is reported only when base cannot
// be indexed: a function, type or method rather than a value in scope.
func (t *galaASTTransformer) checkVariantTypeArgs(base ast.Expr, args iter.Seq[exprSrc]) error {
	for arg := range args {
		text := arg.text()
		if !t.mayNameVariantTypeText(text) {
			continue
		}
		if !t.isInstantiable(base) {
			return nil
		}
		line, col := arg.at().start()
		if err := t.variantTypeArgErrorAt(text, line, col); err != nil {
			return err
		}
	}
	return nil
}

// variantTypeArgSplit splits an explicit type argument's text into its
// pointer prefix (`*`), its name, and its own type arguments: `*Some[int]`
// is ("*", "Some", "[int]").
func variantTypeArgSplit(text string) (ptr, name, typeArgs string) {
	name = strings.TrimLeft(text, "*")
	ptr = text[:len(text)-len(name)]
	if i := strings.IndexByte(name, '['); i >= 0 {
		name, typeArgs = name[:i], name[i:]
	}
	return ptr, name, typeArgs
}

// mayNameVariantTypeText reports whether the explicit type argument written
// as text (GetText) is spelled like a sealed variant and is not a type
// parameter or a value in scope of that name.
func (t *galaASTTransformer) mayNameVariantTypeText(text string) bool {
	_, name, _ := variantTypeArgSplit(text)
	qualifier, bare := splitPackageQualifier(name)
	if !t.variantNames[bare] {
		return false
	}
	if qualifier != "" {
		return true
	}
	_, _, bound := t.scopeLookup(name)
	return !bound && !t.activeTypeParams[name]
}

// variantTypeArgErrorAt returns the GALA-E0061 for an explicit type argument,
// written as text (GetText) from line and col, that names a sealed variant,
// or nil.
func (t *galaASTTransformer) variantTypeArgErrorAt(text string, line, col int) error {
	ptr, name, typeArgs := variantTypeArgSplit(text)
	return t.variantTypeError(name, typeArgs, line, col+len(ptr))
}

// isInstantiable reports whether base, followed by `[...]`, is an
// instantiation rather than an index: a package-level function or type, one
// reached through an import qualifier, or a method.
func (t *galaASTTransformer) isInstantiable(base ast.Expr) bool {
	switch b := base.(type) {
	case *ast.Ident:
		if _, _, bound := t.scopeLookup(b.Name); bound {
			return false
		}
		return t.getFunction(b.Name) != nil || t.getTypeMeta(b.Name) != nil
	case *ast.SelectorExpr:
		if x, ok := b.X.(*ast.Ident); ok {
			if _, _, bound := t.scopeLookup(x.Name); !bound {
				if _, imported := t.importManager.ResolveAlias(x.Name); imported {
					return true
				}
			}
		}
		recv := t.getExprTypeName(b.X)
		if recv.IsNil() {
			return false
		}
		meta := t.getTypeMeta(recv.BaseName())
		if meta == nil {
			return false
		}
		_, isMethod := meta.Methods[b.Sel.Name]
		return isMethod
	}
	return false
}

// variantTypeError returns the GALA-E0061 for a type written as name (bare,
// or `qualifier.Name`) followed by typeArgs, starting at line:col, or nil when
// it does not name a sealed variant.
func (t *galaASTTransformer) variantTypeError(name, typeArgs string, line, col int) error {
	var parent *transpiler.TypeMetadata
	prefix := ""
	if qualifier, bare := splitPackageQualifier(name); qualifier != "" {
		if _, imported := t.importManager.ResolveAlias(qualifier); imported {
			parent = t.findSealedParentForVariant(bare, qualifier)
			prefix = qualifier + "."
		}
	} else if meta := t.getTypeMeta(name); meta != nil && !meta.IsSealed {
		// The name resolves as any other type name does, so std's variants
		// and a dot-imported package's are found, and a type of this package
		// shadows them.
		parent = t.findSealedParentForVariant(meta.Name, meta.Package)
	}
	if parent == nil {
		return nil
	}
	parentType := prefix + parent.Name + typeArgs
	return galaerr.NewCodedSemanticError(
		galaerr.CodeSealedVariantAsType,
		line, col,
		fmt.Sprintf("%s%s is a variant of sealed type %s, not a type", name, typeArgs, parentType),
		fmt.Sprintf("use the sealed type %s here; %s(...) builds one, and `case %s(...)` in a match reaches the variant's fields",
			parentType, name, name),
	).WithSpan(col + len(name))
}
