package analyzer

import (
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The reads of the package's other files on galop's native tree: a sibling
// of the analyzed file, or a file of a package it imports, is read on its
// native tree, as the analyzed file is. The handle rides in the parsed-file
// cache with the file's parse (parsedFileEntry), so every route a sibling
// takes to the analysis (the explicit package files, the sibling cache, a
// directory scan) finds it there.

// add records into o the names other introduces.
func (o ownNames) add(other ownNames) {
	addNames(o.types, other.types)
	for base, methods := range other.methods {
		for name := range methods {
			addMethod(o.methods, base, name)
		}
	}
}

// addName records name into out, as a type's when isType: a name that names
// a type in any file names one.
func addName(out map[string]bool, name string, isType bool) {
	if isType || !out[name] {
		out[name] = isType
	}
}

// addNames records into out each of names (see addName).
func addNames(out, names map[string]bool) {
	for name, isType := range names {
		addName(out, name, isType)
	}
}

// addMethod records into methods the method name of receiver type base.
func addMethod(methods map[string]map[string]bool, base, name string) {
	if methods[base] == nil {
		methods[base] = make(map[string]bool)
	}
	methods[base][name] = true
}

// siblingMethod is a method a sibling declares, as the sibling pass records
// it: its name, its receiver's base type, where its name is, and, set by
// checkSiblingMethod, the key of its receiver type.
type siblingMethod struct {
	name, baseType, fullBaseType string
	pos                          transpiler.SourcePos
}

// checkSiblingMethod keys sm's receiver type and records sm among the
// package's declarations, rejecting it when the analyzed file already
// declares a method of that name on the type. Without this the duplicate
// was silently dropped (the receiver type's Methods map keeps whichever
// definition landed first) and both definitions still reached the generated
// Go, where the Go compiler rejected them with "method already declared".
//
// Reported only when the original is the file being analyzed: the caret then
// lands on real source in the file the diagnostic is rendered against. A
// collision between two siblings is left to the Analyze call for one of
// those two files, which sees it as this same main-file case.
func (a *galaAnalyzer) checkSiblingMethod(sm *siblingMethod, pkgName, absSibPath, sibDisplay string, decls *packageDecls) error {
	sm.fullBaseType = sm.baseType
	if !strings.Contains(sm.baseType, ".") {
		sm.fullBaseType = qualifiedName(pkgName, sm.baseType)
	}
	prev, dup := decls.recordMethod(sm.fullBaseType+"."+sm.name, absSibPath, sm.pos)
	if dup && a.paths.sameFile(prev.file, decls.mainFile) {
		return methodRedefinedError(sm.name, sm.baseType, pkgName,
			prev.pos.Line, prev.pos.Column,
			sibDisplay, decls.mainFile, sm.pos)
	}
	return nil
}

// typeParamScope is the type parameters sm's signature resolves with: its
// receiver's type arguments recvNames, its receiver type's parameters as
// richAST records them so far, and its own, methodTypeParams.
func (sm siblingMethod) typeParamScope(richAST *transpiler.RichAST, recvNames, methodTypeParams []string) []string {
	var typeTypeParams []string
	if typeMeta, ok := richAST.Types[sm.fullBaseType]; ok {
		typeTypeParams = typeMeta.TypeParams
	}
	return methodTypeParamScope(recvNames, typeTypeParams, methodTypeParams)
}

// add records methodMeta, sm's metadata, on its receiver type in richAST,
// creating the type's entry when there is none yet. A method already
// recorded is kept, but takes IsGeneric when methodMeta has it: the sibling
// pass may detect the instantiation-cycle pattern that section 2 missed
// (e.g. when the receiver type was not yet populated in this file's
// richAST). IsGeneric is append-only: once any pass sets it true, later
// passes keep it true. Fixes cross-package stdlib generic method dispatch
// (Array.ZipWithIndex, Array.Grouped, etc.).
func (sm siblingMethod) add(richAST *transpiler.RichAST, methodMeta *transpiler.MethodMetadata, recvNames []string, pkgName string, decls *packageDecls) {
	decls.recordReceiver(methodMeta, recvNames, sm.fullBaseType)
	typeMeta, ok := richAST.Types[sm.fullBaseType]
	if !ok {
		richAST.Types[sm.fullBaseType] = &transpiler.TypeMetadata{
			Name:    sm.baseType,
			Package: pkgName,
			Methods: map[string]*transpiler.MethodMetadata{sm.name: methodMeta},
			Fields:  make(map[string]transpiler.Type),
		}
		return
	}
	if existing, exists := typeMeta.Methods[sm.name]; !exists || existing.OfTypeDecl {
		// A method a file declares replaces one that came with the type's
		// declaration, such as a sealed type's case predicate.
		typeMeta.Methods[sm.name] = methodMeta
	} else if !existing.IsGeneric && methodMeta.IsGeneric {
		existing.IsGeneric = true
	}
}

// siblingFunc is a top-level function a sibling declares: its name, where
// its name is, and, set by checkSiblingFunc, its key.
type siblingFunc struct {
	name, fullName string
	pos            transpiler.SourcePos
}

// checkSiblingFunc keys sf and records it among the package's declarations,
// rejecting it, as checkSiblingMethod does a method, when the analyzed file
// already declares a function of that name: a redeclaration, which was
// previously dropped here without a word.
func (a *galaAnalyzer) checkSiblingFunc(sf *siblingFunc, pkgName, absSibPath, sibDisplay string, decls *packageDecls) error {
	sf.fullName = qualifiedName(pkgName, sf.name)
	prev, dup := decls.recordFunc(sf.fullName, absSibPath, sf.pos)
	if dup && a.paths.sameFile(prev.file, decls.mainFile) {
		return funcRedeclaredError(sf.name, pkgName,
			prev.pos.Line, prev.pos.Column,
			sibDisplay, decls.mainFile, sf.pos)
	}
	return nil
}

// recordSiblingAlias records the alias typeName a sibling declares, whose
// target is written target ("" for a type declaration that is no alias),
// resolved with typeParams in scope. An alias whose target does not resolve
// is left out.
func (a *galaAnalyzer) recordSiblingAlias(richAST *transpiler.RichAST, typeName, target, pkgName string, typeParams []string) {
	if target == "" {
		return
	}
	underlying := a.resolveTypeWithParams(target, pkgName, typeParams)
	if underlying.IsNil() {
		return
	}
	if richAST.TypeAliases == nil {
		richAST.TypeAliases = make(map[string]transpiler.Type)
	}
	richAST.TypeAliases[typeName] = underlying
}

// extractSiblingDeclsNative is extractSiblingFullMetadata's reading of nf,
// the sibling's native tree: it records the sibling's types, sealed and
// opaque types, methods, functions and package-level bindings into sx's
// RichAST.
func (a *galaAnalyzer) extractSiblingDeclsNative(nf nativeFile, sx siblingExtraction) error {
	pkgName, richAST, absSibPath, sibDisplay, docs, decls := sx.pkgName, sx.richAST, sx.absSibPath, sx.sibDisplay, sx.docs, sx.decls
	// 1. Collect struct types with full field info, and shorthand structs.
	for _, decl := range nf.types {
		switch decl.Kind() {
		case native.GalaKIND_typeDeclaration:
			ctx := native.TypeDeclarationNode{GalaNodeRef: decl}
			id := ctx.Identifier()
			typeName := id.GetText()
			fullTypeName := qualifiedName(pkgName, typeName)
			pos := nativePos(id.GalaNodeRef)
			if seen, err := a.siblingDeclSeen(richAST, fullTypeName, typeName, pkgName, hasFields, absSibPath, sibDisplay, pos, decls); err != nil {
				return err
			} else if seen {
				continue
			}
			meta := a.startTypeEntry(richAST, fullTypeName, typeName, pkgName, pos, absSibPath)
			meta.Doc = nativeDocAt(docs, decl)
			meta.TypeParams, meta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)
			if structType := ctx.StructType(); structType.Ok() {
				for fNode := range galaparser.NativeKids(structType.GalaNodeRef, native.GalaKIND_structField) {
					a.recordStructFieldNative(meta, native.StructFieldNode{GalaNodeRef: fNode}, pkgName, docs)
				}
				if meta.DefinedIn == "" {
					meta.DefinedIn = absSibPath
				}
			}
			if ifaceType := ctx.InterfaceType(); ifaceType.Ok() {
				meta.IsInterface = true
				for msNode := range galaparser.NativeKids(ifaceType.GalaNodeRef, native.GalaKIND_methodSpec) {
					msCtx := native.MethodSpecNode{GalaNodeRef: msNode}
					msID := msCtx.Identifier()
					methodName := msID.GetText()
					// Reported in the sibling, so the file that uses the
					// interface does not fail first with a consequence of it.
					if tps, cons := nativeTypeParamsOf(msCtx.TypeParameters()); len(tps) > 0 {
						at := nativePos(msID.GalaNodeRef)
						return galaerr.WithFilePath(genericInterfaceMethodError(at.Line, at.Column, meta, methodName, tps, cons), sibDisplay)
					}
					methodMeta := a.interfaceMethodNative(meta, msCtx, pkgName, absSibPath, docs)
					methodMeta.Pos = nativePos(msID.GalaNodeRef)
					meta.Methods[methodName] = methodMeta
				}
			}
			// Type aliases (e.g., type Handler func(Request) Future[Response]).
			//
			// `typeAlias: identifier | type`, and both branches name an alias:
			// `type Millis int64` and `type Coord Point` take the identifier
			// branch. Reading only the `type` branch would leave those
			// invisible to sibling files and to anything declared above them,
			// so an alias used before its declaration would be reported as an
			// unknown type.
			a.recordSiblingAlias(richAST, typeName, nativeTypeAliasTarget(ctx), pkgName, meta.TypeParams)

		case native.GalaKIND_structShorthandDeclaration:
			ctx := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			id := ctx.Identifier()
			typeName := id.GetText()
			fullTypeName := qualifiedName(pkgName, typeName)
			pos := nativePos(id.GalaNodeRef)
			if seen, err := a.siblingDeclSeen(richAST, fullTypeName, typeName, pkgName, hasFields, absSibPath, sibDisplay, pos, decls); err != nil {
				return err
			} else if seen {
				continue
			}
			meta := a.startTypeEntry(richAST, fullTypeName, typeName, pkgName, pos, absSibPath)
			meta.Doc = nativeDocAt(docs, decl)
			if meta.DefinedIn == "" {
				meta.DefinedIn = absSibPath
			}
			meta.TypeParams, meta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)
			for _, pctx := range nativeParams(ctx.Parameters()) {
				a.recordShorthandFieldNative(meta, pctx, pkgName, decls)
			}
			meta.IsShorthand = true
		}
	}

	// 2. Collect sealed types
	for _, decl := range nf.sealed {
		ctx := native.SealedTypeDeclarationNode{GalaNodeRef: decl}
		id := ctx.Identifier()
		typeName := id.GetText()
		if seen, err := a.siblingDeclSeen(richAST, qualifiedName(pkgName, typeName), typeName, pkgName, isSealedEntry, absSibPath, sibDisplay, nativePos(id.GalaNodeRef), decls); err != nil {
			return err
		} else if seen {
			continue
		}
		if err := a.registerSealedType(nativeSealedDeclOf(ctx, docs), pkgName, absSibPath, richAST); err != nil {
			return err
		}
	}

	// 2.5 Collect opaque types
	for _, decl := range nf.opaque {
		octx := native.OpaqueTypeDeclarationNode{GalaNodeRef: decl}
		id := octx.Identifier()
		typeName := id.GetText()
		if seen, err := a.siblingDeclSeen(richAST, qualifiedName(pkgName, typeName), typeName, pkgName, hasTypeDefinition, absSibPath, sibDisplay, nativePos(id.GalaNodeRef), decls); err != nil {
			return err
		} else if seen {
			continue
		}
		a.registerOpaqueType(nativeOpaqueDeclOf(octx, docs), pkgName, absSibPath, richAST)
	}

	// 3. Collect methods and functions
	for _, decl := range nf.funcs {
		ctx := native.FunctionDeclarationNode{GalaNodeRef: decl}
		id := ctx.Identifier()
		sibPos := nativePos(id.GalaNodeRef)
		sig := ctx.Signature()
		if recvCtx := ctx.Receiver(); recvCtx.Ok() {
			recvType := recvCtx.Type_()
			baseType := nativeBaseTypeName(recvType)
			if baseType == "" {
				continue
			}
			sm := siblingMethod{name: id.GetText(), baseType: baseType, pos: sibPos}
			if err := a.checkSiblingMethod(&sm, pkgName, absSibPath, sibDisplay, decls); err != nil {
				return err
			}
			pointer, ptr := nativeReceiverPointers(recvType)
			methodMeta := &transpiler.MethodMetadata{
				Name:            sm.name,
				Package:         pkgName,
				Doc:             nativeDocAt(docs, decl),
				Pos:             sibPos,
				ReceiverName:    recvCtx.Identifier().GetText(),
				PointerReceiver: pointer,
				PtrReceiver:     ptr,
				DefinedIn:       absSibPath,
			}
			var constraintTexts map[string]string
			methodMeta.TypeParams, constraintTexts = nativeTypeParamsOf(ctx.TypeParameters())
			recvNames := nativeReceiverTypeArgNames(recvType)
			allTypeParams := sm.typeParamScope(richAST, recvNames, methodMeta.TypeParams)
			// A constraint may name the receiver's type parameters too.
			methodMeta.TypeParamConstraints = a.resolveConstraints(allTypeParams, constraintTexts, pkgName)
			methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, allTypeParams)
			// Mirror collectFileFuncsNative's IsGeneric detection so that
			// sibling-extracted methods carry the same function-form flag as
			// methods analyzed directly. Without this, merging a sibling's
			// Analyze result (with IsGeneric=true) on top of an earlier
			// sibling's result (with IsGeneric=false from extraction here)
			// would clobber the true flag and the transpiler would emit
			// method-syntax calls against a standalone-function definition.
			if a.nativeCausesInstantiationCycle(recvType, sig) {
				methodMeta.IsGeneric = true
			}
			params := a.nativeParamsOf(sig.Parameters(), pkgName, allTypeParams)
			methodMeta.ParamTypes, methodMeta.Variadic, methodMeta.ParamNames, methodMeta.DefaultExprs = params.types, params.variadic, params.names, params.defaults
			sm.add(richAST, methodMeta, recvNames, pkgName, decls)
			continue
		}

		sf := siblingFunc{name: id.GetText(), pos: sibPos}
		if err := a.checkSiblingFunc(&sf, pkgName, absSibPath, sibDisplay, decls); err != nil {
			return err
		}
		if _, ok := richAST.Functions[sf.fullName]; ok {
			continue
		}
		funcMeta := &transpiler.FunctionMetadata{
			Name:      sf.name,
			Package:   pkgName,
			Doc:       nativeDocAt(docs, decl),
			Pos:       sibPos,
			DefinedIn: absSibPath,
		}
		funcMeta.TypeParams, funcMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)
		funcMeta.ReturnType, funcMeta.GoResults = a.nativeSignatureResult(sig, pkgName, funcMeta.TypeParams)
		params := a.nativeParamsOf(sig.Parameters(), pkgName, funcMeta.TypeParams)
		funcMeta.ParamTypes, funcMeta.Variadic, funcMeta.ParamNames, funcMeta.DefaultExprs = params.types, params.variadic, params.names, params.defaults
		richAST.Functions[sf.fullName] = funcMeta
	}

	// The sibling's package-level bindings, recorded here rather than by the
	// caller so they reuse the canonical path and docs map sx carries.
	a.extractPackageValsNative(nf, pkgName, richAST, docs, absSibPath)
	return nil
}
