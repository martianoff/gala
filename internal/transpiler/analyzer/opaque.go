package analyzer

import (
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// analyzeOpaqueType records `opaque type UserID int64` (or a phantom-typed
// `opaque type Id[T any] int64`) as TypeMetadata with IsOpaque set and the
// declared Underlying type. The type has no fields; its methods are attached
// by the method-collection pass like any other type's. Methods already
// recorded on a placeholder entry for the name are kept.
//
// The opaque type deliberately does not enter the alias tables: nothing that
// infers, unifies or looks up methods may see through it.
func (a *galaAnalyzer) analyzeOpaqueType(ctx *grammar.OpaqueTypeDeclarationContext, pkgName, definedIn string, richAST *transpiler.RichAST, docs map[int]string) {
	typeName := ctx.Identifier().GetText()
	fullTypeName := typeName
	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		fullTypeName = pkgName + "." + typeName
	}

	meta := &transpiler.TypeMetadata{
		Name:      typeName,
		Package:   pkgName,
		Doc:       docAt(docs, ctx.GetStart()),
		Pos:       transpiler.PosFromToken(ctx.Identifier().GetStart()),
		Methods:   make(map[string]*transpiler.MethodMetadata),
		Fields:    make(map[string]transpiler.Type),
		IsOpaque:  true,
		DefinedIn: definedIn,
	}
	if existing, ok := richAST.Types[fullTypeName]; ok {
		for k, v := range existing.Methods {
			meta.Methods[k] = v
		}
	}

	if ctx.TypeParameters() != nil {
		tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
		if tpList := tpCtx.TypeParameterList(); tpList != nil {
			for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
				tpc := tp.(*grammar.TypeParameterContext)
				tpName := tpc.Identifier(0).GetText()
				meta.TypeParams = append(meta.TypeParams, tpName)
				if len(tpc.AllIdentifier()) > 1 {
					if meta.TypeParamConstraints == nil {
						meta.TypeParamConstraints = make(map[string]string)
					}
					meta.TypeParamConstraints[tpName] = tpc.Identifier(1).GetText()
				}
			}
		}
	}

	if ctx.Type_() != nil {
		meta.Underlying = a.resolveTypeWithParams(ctx.Type_().GetText(), pkgName, meta.TypeParams)
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
