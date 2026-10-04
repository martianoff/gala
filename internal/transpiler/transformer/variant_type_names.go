package transformer

import (
	"fmt"
	"go/ast"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"

	"github.com/antlr4-go/antlr/v4"
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
// checkVariantTypeNames walks every type the grammar parses as one —
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
// written, and the node it was written as.
type variantTypeRef struct {
	name     string
	typeArgs string
	node     antlr.ParserRuleContext
}

// checkVariantTypeNames reports the first type in tree that names a sealed
// variant, or nil when there is none.
func (t *galaASTTransformer) checkVariantTypeNames(tree antlr.Tree) error {
	// A type declared in this file shadows a variant of the same name.
	// Scoping is not tracked for types: one declared anywhere in the file
	// (a function may declare a local type) is never reported, which can
	// miss a variant but cannot reject a valid program. A type parameter is
	// scoped to its declaration; see typeParamInScope.
	declared := make(map[string]bool)
	var refs []variantTypeRef
	ref := func(ids []grammar.IIdentifierContext, typeArgs grammar.ITypeArgumentsContext, node antlr.ParserRuleContext) {
		if !t.variantNames[ids[len(ids)-1].GetText()] {
			return
		}
		r := variantTypeRef{name: ids[0].GetText(), node: node}
		if len(ids) == 2 {
			r.name += "." + ids[1].GetText()
		}
		if typeArgs != nil {
			r.typeArgs = typeArgs.GetText()
		}
		refs = append(refs, r)
	}
	walkTree(tree, func(node antlr.Tree) {
		switch n := node.(type) {
		case *grammar.TypeDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.StructShorthandDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.SealedTypeDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.OpaqueTypeDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.TypeContext:
			if qid, ok := n.QualifiedIdentifier().(*grammar.QualifiedIdentifierContext); ok && qid != nil {
				if ids := qid.AllIdentifier(); len(ids) <= 2 {
					ref(ids, n.TypeArguments(), qid)
				}
			}
		case *grammar.ParameterContext:
			// A function type's parameters written without names, as in
			// `func(Circle) int`, parse as names with no type; the function
			// type uses each name as the type.
			if n.Type_() == nil && n.Identifier() != nil && isFuncTypeSignature(n) {
				ref([]grammar.IIdentifierContext{n.Identifier()}, nil, n)
			}
		case *grammar.TypeAliasContext:
			// `type C Circle` parses as the alias's bare-identifier
			// alternative, which is not a TypeContext.
			if id := n.Identifier(); id != nil {
				ref([]grammar.IIdentifierContext{id}, nil, n)
			}
		}
	})
	for _, r := range refs {
		if declared[r.name] || typeParamInScope(r.node, r.name) {
			continue
		}
		start := r.node.GetStart()
		if err := t.variantTypeError(r.name, r.typeArgs, start.GetLine(), start.GetColumn()); err != nil {
			return err
		}
	}
	return nil
}

// typeParamInScope reports whether a declaration enclosing node declares a
// type parameter called name — in its type parameter list, or, for a method,
// as a type argument of its receiver (`func (b Box[Left]) Get() Left`).
func typeParamInScope(node antlr.Tree, name string) bool {
	for ; node != nil; node = node.GetParent() {
		if fn, ok := node.(*grammar.FunctionDeclarationContext); ok && fn.Receiver() != nil &&
			slices.Contains(receiverTypeParamNames(fn.Receiver().(*grammar.ReceiverContext).Type_()), name) {
			return true
		}
		decl, ok := node.(interface {
			TypeParameters() grammar.ITypeParametersContext
		})
		if !ok || decl.TypeParameters() == nil {
			continue
		}
		list := decl.TypeParameters().TypeParameterList()
		for _, tp := range list.AllTypeParameter() {
			if tp.Identifier(0).GetText() == name {
				return true
			}
		}
	}
	return false
}

// isFuncTypeSignature reports whether parameter p belongs to the signature of
// a function type (`func(...)` in a type position) rather than of a function,
// method or lambda, whose untyped parameters are names.
func isFuncTypeSignature(p *grammar.ParameterContext) bool {
	// parameter → parameterList → parameters → signature → type
	_, ok := p.GetParent().GetParent().GetParent().GetParent().(*grammar.TypeContext)
	return ok
}

// checkVariantTypeArgs reports a sealed variant written as an explicit type
// argument of base, as in `ArrayOf[Circle](...)`. The grammar reads type
// arguments as an index, so a variant name is reported only when base cannot
// be indexed: a function, type or method rather than a value in scope.
func (t *galaASTTransformer) checkVariantTypeArgs(base ast.Expr, args []grammar.IExpressionContext) error {
	for _, arg := range args {
		if !t.mayNameVariantType(arg) {
			continue
		}
		if !t.isInstantiable(base) {
			return nil
		}
		if err := t.variantTypeArgError(arg); err != nil {
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

// mayNameVariantType reports whether the explicit type argument arg is
// spelled like a sealed variant and is not a type parameter or a value in
// scope of that name.
func (t *galaASTTransformer) mayNameVariantType(arg grammar.IExpressionContext) bool {
	_, name, _ := variantTypeArgSplit(arg.GetText())
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

// variantTypeArgError returns the GALA-E0061 for an explicit type argument
// that names a sealed variant, or nil.
func (t *galaASTTransformer) variantTypeArgError(arg grammar.IExpressionContext) error {
	ptr, name, typeArgs := variantTypeArgSplit(arg.GetText())
	start := arg.GetStart()
	return t.variantTypeError(name, typeArgs, start.GetLine(), start.GetColumn()+len(ptr))
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

// walkTree calls visit on node and every node below it, in source order.
func walkTree(node antlr.Tree, visit func(antlr.Tree)) {
	visit(node)
	for _, child := range node.GetChildren() {
		walkTree(child, visit)
	}
}
