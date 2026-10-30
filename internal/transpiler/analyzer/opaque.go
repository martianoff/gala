package analyzer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// opaqueDecl is what analyzeOpaqueType reads from an opaque type
// declaration: its name, doc comment, the position of its name, its type
// parameters with their constraints, and its underlying type as written
// (parser.TypeText), "" when it has none.
type opaqueDecl struct {
	name                 string
	doc                  string
	pos                  transpiler.SourcePos
	typeParams           []string
	typeParamConstraints map[string]string
	underlying           string
}

// opaqueDeclOf reads ctx, with the doc comments of its file.
func opaqueDeclOf(ctx *grammar.OpaqueTypeDeclarationContext, docs galaparser.Docs) opaqueDecl {
	d := opaqueDecl{
		name: ctx.Identifier().GetText(),
		doc:  docAt(docs, ctx.GetStart()),
		pos:  transpiler.PosFromToken(ctx.Identifier().GetStart()),
	}
	d.typeParams, d.typeParamConstraints = typeParamsOf(ctx.TypeParameters())
	if ctx.Type_() != nil {
		d.underlying = galaparser.TypeText(ctx.Type_())
	}
	return d
}

// analyzeOpaqueType records an opaque type declaration (see
// registerOpaqueType).
func (a *galaAnalyzer) analyzeOpaqueType(ctx *grammar.OpaqueTypeDeclarationContext, pkgName, definedIn string, richAST *transpiler.RichAST, docs galaparser.Docs) {
	a.registerOpaqueType(opaqueDeclOf(ctx, docs), pkgName, definedIn, richAST)
}

// registerOpaqueType records `opaque type UserID int64` (or a phantom-typed
// `opaque type Id[T any] int64`) as TypeMetadata with IsOpaque set and the
// declared Underlying type. The type has no fields; its methods are attached
// by the method-collection pass like any other type's. Methods already
// recorded on a placeholder entry for the name are kept.
//
// The opaque type deliberately does not enter the alias tables: nothing that
// infers, unifies or looks up methods may see through it.
func (a *galaAnalyzer) registerOpaqueType(decl opaqueDecl, pkgName, definedIn string, richAST *transpiler.RichAST) {
	typeName := decl.name
	fullTypeName := qualifiedName(pkgName, typeName)

	meta := &transpiler.TypeMetadata{
		Name:                 typeName,
		Package:              pkgName,
		Doc:                  decl.doc,
		Pos:                  decl.pos,
		Methods:              make(map[string]*transpiler.MethodMetadata),
		Fields:               make(map[string]transpiler.Type),
		IsOpaque:             true,
		DefinedIn:            definedIn,
		TypeParams:           decl.typeParams,
		TypeParamConstraints: decl.typeParamConstraints,
	}
	if existing, ok := richAST.Types[fullTypeName]; ok {
		for k, v := range existing.Methods {
			meta.Methods[k] = v
		}
	}

	if decl.underlying != "" {
		meta.Underlying = a.resolveTypeWithParams(decl.underlying, pkgName, meta.TypeParams)
	}
	richAST.Types[fullTypeName] = meta
}

// recordOpaqueBases sets UnderlyingBase on each opaque type pkgName declares:
// its Underlying followed through aliases (resolve, from
// declaredTypeUnderlying) and Go named types — the package's own hand-written
// .go types included — to the type Go sees at the bottom. Tools that present
// the type (hover, gala doc) read it to tell, for one, whether Compare is
// generated, without re-deriving the package's alias namespace.
func recordOpaqueBases(richAST *transpiler.RichAST, pkgName string, resolve func(transpiler.Type) transpiler.Type) {
	for _, meta := range richAST.Types {
		if meta == nil || !meta.IsOpaque || meta.Package != pkgName || meta.Underlying == nil {
			continue
		}
		meta.UnderlyingBase = opaqueBase(richAST, pkgName, resolve, meta.Underlying)
	}
}

func opaqueBase(richAST *transpiler.RichAST, pkgName string, resolve func(transpiler.Type) transpiler.Type, typ transpiler.Type) transpiler.Type {
	for hop := 0; hop < 16; hop++ {
		typ = resolve(typ)
		basic, ok := typ.(transpiler.BasicType)
		if !ok || transpiler.IsPrimitiveType(basic.Name) {
			break
		}
		// A bare name declared by a .go file of this package.
		td := richAST.GoTypeInfo.GetTypeData(pkgName + "." + basic.Name)
		if td == nil || td.Underlying == nil || td.Underlying.String() == typ.String() {
			break
		}
		typ = td.Underlying
	}
	return typ
}
