package transformer

import (
	"fmt"
	"go/ast"
	"regexp"
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
// alias target, composite literal — once the import set is known, so a
// variant of an imported sealed type (`Some[int]`, `shapes.Circle`) is caught
// too. A call's explicit type arguments (`ArrayOf[Circle](...)`) parse as an
// index expression, so resolveIndexAccess checks them through
// checkVariantTypeArgs, where scope tells an instantiation from an index.

// checkVariantTypeNames reports the first type in tree that names a sealed
// variant, or nil when there is none.
func (t *galaASTTransformer) checkVariantTypeNames(tree antlr.Tree) error {
	// A type or type parameter declared in this file shadows a variant of the
	// same name wherever it is in scope. Scoping is not tracked: a name
	// declared anywhere in the file is never reported, which can miss a
	// variant but cannot reject a valid program.
	declared := make(map[string]bool)
	walkTree(tree, func(node antlr.Tree) {
		var id grammar.IIdentifierContext
		switch n := node.(type) {
		case *grammar.TypeDeclarationContext:
			id = n.Identifier()
		case *grammar.StructShorthandDeclarationContext:
			id = n.Identifier()
		case *grammar.SealedTypeDeclarationContext:
			id = n.Identifier()
		case *grammar.TypeParameterContext:
			id = n.Identifier(0)
		}
		if id != nil {
			declared[id.GetText()] = true
		}
	})
	check := func(name, typeArgs string, start antlr.Token) error {
		if declared[name] {
			return nil
		}
		return t.variantTypeError(name, typeArgs, start)
	}

	var found error
	walkTree(tree, func(node antlr.Tree) {
		if found != nil {
			return
		}
		switch n := node.(type) {
		case *grammar.TypeContext:
			if qid := n.QualifiedIdentifier(); qid != nil {
				typeArgs := ""
				if n.TypeArguments() != nil {
					typeArgs = n.TypeArguments().GetText()
				}
				found = check(qid.GetText(), typeArgs, qid.GetStart())
				return
			}
			// A function type's parameters written without names, as in
			// `func(Circle) int`, parse as names with no type; the function
			// type uses each name as the type.
			sig, ok := n.Signature().(*grammar.SignatureContext)
			if !ok || sig == nil {
				return
			}
			list, ok := sig.Parameters().(*grammar.ParametersContext).ParameterList().(*grammar.ParameterListContext)
			if !ok || list == nil {
				return
			}
			for _, p := range list.AllParameter() {
				if param := p.(*grammar.ParameterContext); param.Type_() == nil && param.Identifier() != nil && found == nil {
					found = check(param.Identifier().GetText(), "", param.GetStart())
				}
			}
		case *grammar.TypeAliasContext:
			// `type C Circle` parses as the alias's bare-identifier
			// alternative, which is not a TypeContext.
			if id := n.Identifier(); id != nil {
				found = check(id.GetText(), "", id.GetStart())
			}
		}
	})
	return found
}

// typeArgExprPattern matches an explicit type argument written as a name,
// optionally package-qualified and instantiated: `Circle`, `sh.Circle`,
// `Some[int]`.
var typeArgExprPattern = regexp.MustCompile(`^([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?)(\[.*\])?$`)

// checkVariantTypeArgs reports a sealed variant written as an explicit type
// argument of base, as in `ArrayOf[Circle](...)`. The grammar reads type
// arguments as an index, so this runs only when base cannot be indexed: a
// function, type or method rather than a value in scope.
func (t *galaASTTransformer) checkVariantTypeArgs(base ast.Expr, args []grammar.IExpressionContext) error {
	if !t.isInstantiable(base) {
		return nil
	}
	for _, arg := range args {
		m := typeArgExprPattern.FindStringSubmatch(arg.GetText())
		if m == nil || t.activeTypeParams[m[1]] {
			continue
		}
		if err := t.variantTypeError(m[1], m[2], arg.GetStart()); err != nil {
			return err
		}
	}
	return nil
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
				_, imported := t.importManager.ResolveAlias(x.Name)
				return imported
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
// or `qualifier.Name`) followed by typeArgs, starting at start, or nil when
// it does not name a sealed variant.
func (t *galaASTTransformer) variantTypeError(name, typeArgs string, start antlr.Token) error {
	var parent *transpiler.TypeMetadata
	prefix := ""
	if qualifier, bare, qualified := strings.Cut(name, "."); qualified {
		if pkg, ok := t.importManager.ResolveAlias(qualifier); ok {
			parent = t.sealedParentInPackage(bare, pkg)
			prefix = qualifier + "."
		}
	} else {
		parent = t.sealedParentOfVariantType(name)
	}
	if parent == nil {
		return nil
	}
	parentType := prefix + parent.Name + typeArgs
	return galaerr.NewCodedSemanticError(
		galaerr.CodeSealedVariantAsType,
		start.GetLine(), start.GetColumn(),
		fmt.Sprintf("%s%s is a variant of sealed type %s, not a type", name, typeArgs, parentType),
		fmt.Sprintf("use the sealed type %s here; %s(...) builds one, and `case %s(...)` in a match reaches the variant's fields",
			parentType, name, name),
	).WithSpan(start.GetColumn() + len(name))
}

// sealedParentOfVariantType returns the sealed type that declares the
// unqualified variant name, or nil. The name resolves as any other type name
// does, so std's variants and a dot-imported package's are found, and a type
// of this package shadows them.
func (t *galaASTTransformer) sealedParentOfVariantType(name string) *transpiler.TypeMetadata {
	meta := t.getTypeMeta(name)
	if meta == nil || meta.IsSealed {
		return nil
	}
	return t.sealedParentInPackage(meta.Name, meta.Package)
}

// sealedParentInPackage returns the sealed type of package pkg that declares
// variant name, or nil.
func (t *galaASTTransformer) sealedParentInPackage(name, pkg string) *transpiler.TypeMetadata {
	for _, meta := range t.typeMetas {
		if !meta.IsSealed || meta.Package != pkg {
			continue
		}
		for _, sv := range meta.SealedVariants {
			if sv.Name == name {
				return meta
			}
		}
	}
	return nil
}

// walkTree calls visit on node and every node below it, in source order.
func walkTree(node antlr.Tree, visit func(antlr.Tree)) {
	visit(node)
	for _, child := range node.GetChildren() {
		walkTree(child, visit)
	}
}
