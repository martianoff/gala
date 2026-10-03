package transformer

import (
	"fmt"
	"go/ast"
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
// lowers speculatively and discards. A call's explicit type arguments
// (`ArrayOf[Circle](...)`) parse as an index expression, so resolveIndexAccess
// checks them through checkVariantTypeArgs, where scope tells an
// instantiation from an index.

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
// written, and where it starts.
type variantTypeRef struct {
	name, typeArgs string
	start          antlr.Token
}

// checkVariantTypeNames reports the first type in tree that names a sealed
// variant, or nil when there is none.
func (t *galaASTTransformer) checkVariantTypeNames(tree antlr.Tree) error {
	// A type or type parameter declared in this file shadows a variant of the
	// same name wherever it is in scope. Scoping is not tracked: a name
	// declared anywhere in the file is never reported, which can miss a
	// variant but cannot reject a valid program.
	declared := make(map[string]bool)
	var refs []variantTypeRef
	ref := func(name, typeArgs string, start antlr.Token) {
		if _, bare := splitPackageQualifier(name); t.variantNames[bare] {
			refs = append(refs, variantTypeRef{name, typeArgs, start})
		}
	}
	walkTree(tree, func(node antlr.Tree) {
		switch n := node.(type) {
		case *grammar.TypeDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.StructShorthandDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.SealedTypeDeclarationContext:
			declared[n.Identifier().GetText()] = true
		case *grammar.TypeParameterContext:
			declared[n.Identifier(0).GetText()] = true
		case *grammar.TypeContext:
			if qid := n.QualifiedIdentifier(); qid != nil {
				typeArgs := ""
				if n.TypeArguments() != nil {
					typeArgs = n.TypeArguments().GetText()
				}
				ref(qid.GetText(), typeArgs, qid.GetStart())
			}
		case *grammar.ParameterContext:
			// A function type's parameters written without names, as in
			// `func(Circle) int`, parse as names with no type; the function
			// type uses each name as the type.
			if n.Type_() == nil && n.Identifier() != nil && isFuncTypeSignature(n) {
				ref(n.Identifier().GetText(), "", n.GetStart())
			}
		case *grammar.TypeAliasContext:
			// `type C Circle` parses as the alias's bare-identifier
			// alternative, which is not a TypeContext.
			if id := n.Identifier(); id != nil {
				ref(id.GetText(), "", id.GetStart())
			}
		}
	})
	for _, r := range refs {
		if declared[r.name] {
			continue
		}
		if err := t.variantTypeError(r.name, r.typeArgs, r.start); err != nil {
			return err
		}
	}
	return nil
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
		text := arg.GetText()
		name, typeArgs := text, ""
		if i := strings.IndexByte(text, '['); i >= 0 {
			name, typeArgs = text[:i], text[i:]
		}
		if _, bare := splitPackageQualifier(name); !t.variantNames[bare] || t.activeTypeParams[name] {
			continue
		}
		if !t.isInstantiable(base) {
			return nil
		}
		if err := t.variantTypeError(name, typeArgs, arg.GetStart()); err != nil {
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
		start.GetLine(), start.GetColumn(),
		fmt.Sprintf("%s%s is a variant of sealed type %s, not a type", name, typeArgs, parentType),
		fmt.Sprintf("use the sealed type %s here; %s(...) builds one, and `case %s(...)` in a match reaches the variant's fields",
			parentType, name, name),
	).WithSpan(start.GetColumn() + len(name))
}

// walkTree calls visit on node and every node below it, in source order.
func walkTree(node antlr.Tree, visit func(antlr.Tree)) {
	visit(node)
	for _, child := range node.GetChildren() {
		walkTree(child, visit)
	}
}
