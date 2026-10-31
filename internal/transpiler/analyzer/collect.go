package analyzer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// fileDecls is what analyze's collection of the analyzed file's own
// declarations works on: the metadata it fills, the file it reads and where
// that file sits, the file's doc comments, and the declarations seen so far
// in the package (see packageDecls). The collection runs on ANTLR's tree
// (collectFileTypes, collectFileFuncs and the functions after them) or on the
// native tree (their twins in collect_native.go), never on both.
type fileDecls struct {
	richAST              *transpiler.RichAST
	pkgName              string
	filePath             string
	absFilePath          string
	docs                 galaparser.Docs
	decls                *packageDecls
	pendingDefaultChecks []pendingDefaultCheck
}

// pendingDefaultCheck is a function whose parameter defaults analyze
// validates after section 2.5, when every declared type of the package can be
// resolved: its metadata, where its declaration starts, and the span of each
// default.
type pendingDefaultCheck struct {
	meta      *transpiler.FunctionMetadata
	line, col int
	spans     map[int]defaultExprSpan
}

// qualifiedName is name keyed in package pkgName: qualified unless the
// package is main or test, or there is none.
func qualifiedName(pkgName, name string) string {
	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		return pkgName + "." + name
	}
	return name
}

// collectFileTypes records the struct, shorthand-struct and interface types
// sourceFile declares (analyze's section 1).
func (a *galaAnalyzer) collectFileTypes(sourceFile *grammar.SourceFileContext, fd *fileDecls) error {
	richAST, pkgName, filePath, absFilePath, docs, decls := fd.richAST, fd.pkgName, fd.filePath, fd.absFilePath, fd.docs, fd.decls
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if typeDecl := topDecl.TypeDeclaration(); typeDecl != nil {
			ctx := typeDecl.(*grammar.TypeDeclarationContext)
			typeName := ctx.Identifier().GetText()

			// The std names the transpiler gives built-in meaning are reserved;
			// any other std type is shadowed by the declaration.
			if err := CheckStdConflict(typeName, pkgName); err != nil {
				return err
			}

			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}

			var meta *transpiler.TypeMetadata
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				// Error if type is being redefined from a DIFFERENT file.
				// Skip if DefinedIn is empty (cache) or same file (re-analysis from analyzePackage).
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
					return typeRedefinedError(typeName, pkgName,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						existing.DefinedIn, filePath, existing.Pos)
				}
				meta = existing
				// Clear fields to avoid duplicates if re-analyzing
				meta.IsInterface = false
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
				meta.StoredAsDeclared = nil
				meta.FieldPositions = nil
				meta.FieldDocs = nil
				meta.Pos = transpiler.PosFromToken(ctx.Identifier().GetStart())
				setDoc(&meta.Doc, docs, ctx.GetStart())
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Doc:     docAt(docs, ctx.GetStart()),
					Pos:     transpiler.PosFromToken(ctx.Identifier().GetStart()),
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				richAST.Types[fullTypeName] = meta
			}

			// Assigned, not appended: a reused entry already holds them.
			meta.TypeParams, meta.TypeParamConstraints = typeParamsOf(ctx.TypeParameters())

			if ctx.StructType() != nil {
				structType := ctx.StructType().(*grammar.StructTypeContext)
				for _, field := range structType.AllStructField() {
					fctx := field.(*grammar.StructFieldContext)
					fieldName := fctx.Identifier().GetText()
					// Reject duplicate field names within a single struct
					// declaration. Without this, the second declaration
					// silently overwrote the first in the Fields map and
					// duplicated the name in FieldNames, producing invalid
					// Go output. This guard is scoped to one parse of one
					// struct (Fields was cleared for this declaration above),
					// so it does not need an existing.DefinedIn check.
					if _, exists := meta.Fields[fieldName]; exists {
						return galaerr.NewCodedSemanticError(
							galaerr.CodeStructFieldRedeclared,
							fctx.Identifier().GetStart().GetLine(), fctx.Identifier().GetStart().GetColumn(),
							fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
							"rename or remove the duplicate field",
						)
					}
					meta.Fields[fieldName] = a.resolveTypeWithParams(galaparser.TypeText(fctx.Type_()), pkgName, meta.TypeParams)
					meta.FieldNames = append(meta.FieldNames, fieldName)
					meta.ImmutFlags = append(meta.ImmutFlags, fctx.VAR() == nil)
					if meta.FieldPositions == nil {
						meta.FieldPositions = make(map[string]transpiler.SourcePos)
					}
					meta.FieldPositions[fieldName] = transpiler.PosFromToken(fctx.Identifier().GetStart())
					addFieldDoc(meta, fieldName, docAt(docs, fctx.GetStart()))
				}
				meta.DefinedIn = filePath
			}

			// Extract interface method signatures as type methods
			if ctx.InterfaceType() != nil {
				meta.IsInterface = true
				ifaceType := ctx.InterfaceType().(*grammar.InterfaceTypeContext)
				// seenSpecs is a per-declaration dedup so re-analysis (which
				// preserves the Methods map across calls) does not falsely
				// trigger the redeclaration check on already-registered
				// specs. Within ONE iteration of AllMethodSpec() we still
				// catch genuine duplicates.
				seenSpecs := make(map[string]bool)
				for _, ms := range ifaceType.AllMethodSpec() {
					msCtx := ms.(*grammar.MethodSpecContext)
					methodName := msCtx.Identifier().GetText()
					if seenSpecs[methodName] {
						return galaerr.NewCodedSemanticError(
							galaerr.CodeInterfaceMethodRedeclared,
							msCtx.Identifier().GetStart().GetLine(), msCtx.Identifier().GetStart().GetColumn(),
							fmt.Sprintf("method %q already declared in interface %q", methodName, typeName),
							"rename or remove the duplicate method",
						)
					}
					seenSpecs[methodName] = true
					if tps, cons := typeParamsOf(msCtx.TypeParameters()); len(tps) > 0 {
						id := msCtx.Identifier().GetStart()
						return genericInterfaceMethodError(id.GetLine(), id.GetColumn(), meta, methodName, tps, cons)
					}
					methodMeta := &transpiler.MethodMetadata{
						Name:      methodName,
						Package:   pkgName,
						Doc:       docAt(docs, msCtx.GetStart()),
						DefinedIn: filePath,
					}
					methodMeta.ReturnType, methodMeta.GoResults = a.signatureResult(msCtx.Signature(), pkgName, meta.TypeParams)
					if msCtx.Signature().Parameters() != nil {
						pCtx := msCtx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(galaparser.TypeText(paramCtx.Type_()), pkgName, meta.TypeParams))
									methodMeta.Variadic = paramCtx.ELLIPSIS() != nil
								} else {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
								}
							}
						}
					}
					meta.Methods[methodName] = methodMeta
				}
			}
		}

		if shorthandCtx := topDecl.StructShorthandDeclaration(); shorthandCtx != nil {
			ctx := shorthandCtx.(*grammar.StructShorthandDeclarationContext)
			typeName := ctx.Identifier().GetText()

			// Reserved std names: see the type declaration case above.
			if err := CheckStdConflict(typeName, pkgName); err != nil {
				return err
			}

			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}

			var meta *transpiler.TypeMetadata
			pos := transpiler.PosFromToken(ctx.Identifier().GetStart())
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
					return typeRedefinedError(typeName, pkgName,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						existing.DefinedIn, filePath, existing.Pos)
				}
				meta = existing
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
				meta.StoredAsDeclared = nil
				meta.FieldPositions = nil
				meta.FieldDocs = nil
				meta.FieldDefaults = nil
				meta.Pos = pos
				setDoc(&meta.Doc, docs, ctx.GetStart())
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Doc:     docAt(docs, ctx.GetStart()),
					Pos:     pos,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				richAST.Types[fullTypeName] = meta
			}

			// Assigned, not appended: a reused entry already holds them.
			meta.TypeParams, meta.TypeParamConstraints = typeParamsOf(ctx.TypeParameters())

			if ctx.Parameters() != nil {
				paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
				if paramsCtx.ParameterList() != nil {
					for _, param := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
						pctx := param.(*grammar.ParameterContext)
						fieldName := pctx.Identifier().GetText()
						// Reject duplicate field names within a shorthand
						// struct declaration. Fields was cleared above on
						// re-analysis, so this check is scoped to one parse.
						if _, exists := meta.Fields[fieldName]; exists {
							return galaerr.NewCodedSemanticError(
								galaerr.CodeStructFieldRedeclared,
								pctx.Identifier().GetStart().GetLine(), pctx.Identifier().GetStart().GetColumn(),
								fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
								"rename or remove the duplicate field",
							)
						}
						fieldType := ""
						if pctx.Type_() != nil {
							fieldType = galaparser.TypeText(pctx.Type_())
						}
						decls.recordShorthandField(meta, fieldName, a.resolveTypeWithParams(fieldType, pkgName, meta.TypeParams), pctx.VAL() != nil, pctx.VAR() != nil)
						if meta.FieldPositions == nil {
							meta.FieldPositions = make(map[string]transpiler.SourcePos)
						}
						meta.FieldPositions[fieldName] = transpiler.PosFromToken(pctx.Identifier().GetStart())
						recordFieldDefault(meta, fieldName, pctx)
					}
				}
				meta.IsShorthand = true
				meta.DefinedIn = filePath
			}
		}
	}
	return nil
}

// collectFileFuncs records the sealed and opaque types, methods and top-level
// functions sourceFile declares (analyze's sections 1.5 to 2), and queues on
// fd the functions and sealed cases whose parameter defaults analyze checks
// once the siblings are in.
func (a *galaAnalyzer) collectFileFuncs(sourceFile *grammar.SourceFileContext, fd *fileDecls) error {
	richAST, pkgName, filePath, absFilePath, docs, decls := fd.richAST, fd.pkgName, fd.filePath, fd.absFilePath, fd.docs, fd.decls
	// 1.5 Collect sealed types.
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if sealedCtx := topDecl.SealedTypeDeclaration(); sealedCtx != nil {
			ctx := sealedCtx.(*grammar.SealedTypeDeclarationContext)
			sealedName := ctx.Identifier().GetText()
			fullSealedName := sealedName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullSealedName = pkgName + "." + sealedName
			}
			// Check for redefinition (skip if DefinedIn is empty — type came from cache)
			if existing, ok := richAST.Types[fullSealedName]; ok && existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
				return typeRedefinedError(sealedName, pkgName,
					ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
					existing.DefinedIn, filePath, existing.Pos)
			}
			if err := a.analyzeSealedType(ctx, pkgName, richAST, docs); err != nil {
				return err
			}
			fd.sealedDeclaredHere(fullSealedName)
		}
	}

	// 1.6 Collect opaque types
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		octx, ok := topDecl.OpaqueTypeDeclaration().(*grammar.OpaqueTypeDeclarationContext)
		if !ok || octx == nil {
			continue
		}
		typeName := octx.Identifier().GetText()
		if err := CheckStdConflict(typeName, pkgName); err != nil {
			return err
		}
		fullTypeName := typeName
		if pkgName != "" && pkgName != "main" && pkgName != "test" {
			fullTypeName = pkgName + "." + typeName
		}
		if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName && existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
			return typeRedefinedError(typeName, pkgName,
				octx.GetStart().GetLine(), octx.GetStart().GetColumn(),
				existing.DefinedIn, filePath, existing.Pos)
		}
		a.analyzeOpaqueType(octx, pkgName, filePath, richAST, docs)
	}

	// 2. Collect methods and functions
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if funcDeclCtx := topDecl.FunctionDeclaration(); funcDeclCtx != nil {
			ctx := funcDeclCtx.(*grammar.FunctionDeclarationContext)
			if ctx.Receiver() != nil {
				recvCtx := ctx.Receiver().(*grammar.ReceiverContext)
				baseType := getBaseTypeName(recvCtx.Type_())
				if baseType != "" {
					methodName := ctx.Identifier().GetText()
					fullBaseType := baseType
					if pkgName != "" && pkgName != "main" && pkgName != "test" && !strings.Contains(baseType, ".") {
						fullBaseType = pkgName + "." + baseType
					}

					methodMeta := &transpiler.MethodMetadata{
						Name:            methodName,
						Package:         pkgName,
						Doc:             docAt(docs, ctx.GetStart()),
						Pos:             transpiler.PosFromToken(ctx.Identifier().GetStart()),
						ReceiverName:    recvCtx.Identifier().GetText(),
						PointerReceiver: isPointerReceiver(recvCtx),
						PtrReceiver:     isPtrReceiver(recvCtx),
					}
					methodMeta.TypeParams, methodMeta.TypeParamConstraints = a.declaredTypeParams(ctx.TypeParameters(), pkgName)

					// Collect receiver's type parameters to include when resolving types
					// e.g., for "func (s Some[T]) Unapply(o Option[T])", we need to know T is a type param
					var typeTypeParams []string
					if typeMeta, ok := richAST.Types[fullBaseType]; ok {
						typeTypeParams = typeMeta.TypeParams
					}
					recvNames := receiverTypeArgNames(recvCtx.Type_())
					allTypeParams := methodTypeParamScope(recvNames, typeTypeParams, methodMeta.TypeParams)

					methodMeta.ReturnType, methodMeta.GoResults = a.signatureResult(ctx.Signature(), pkgName, allTypeParams)
					// Detect Go generics instantiation cycle:
					// If receiver is Container[T] and return is Container[SomeType[T, ...]]
					// Go would detect infinite type instantiation
					if a.causesInstantiationCycle(recvCtx.Type_(), ctx.Signature()) {
						methodMeta.IsGeneric = true
					}

					if ctx.Signature().Parameters() != nil {
						pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for i, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(galaparser.TypeText(paramCtx.Type_()), pkgName, allTypeParams))
									methodMeta.Variadic = paramCtx.ELLIPSIS() != nil
								} else {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
								}
								if paramCtx.Identifier() != nil {
									methodMeta.ParamNames = append(methodMeta.ParamNames, paramCtx.Identifier().GetText())
								} else {
									methodMeta.ParamNames = append(methodMeta.ParamNames, "")
								}
								recordParamDefault(&methodMeta.DefaultExprs, i, paramCtx)
							}
						}
					}

					// The signature takes its type's parameter names after
					// section 2.5, when every file's types are known.
					decls.recordReceiver(methodMeta, recvNames, fullBaseType)

					if typeMeta, ok := richAST.Types[fullBaseType]; ok {
						if existing, exists := typeMeta.Methods[methodName]; exists {
							// Error if method already has a user-defined implementation
							if existing.DefinedIn != "" && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
								return methodRedefinedError(methodName, baseType, pkgName,
									ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
									existing.DefinedIn, filePath, existing.Pos)
							}
							// Merge IsGeneric: preserve a pre-populated flag (e.g. from a
							// sibling-metadata pass) without clobbering a fresh true that
							// causesInstantiationCycle just set. Either source may legitimately
							// flag the method as needing function-form generation; once flagged,
							// never flip back to false (Go generation would emit method syntax
							// against a function definition).
							methodMeta.IsGeneric = methodMeta.IsGeneric || existing.IsGeneric
						}
						methodMeta.DefinedIn = filePath
						typeMeta.Methods[methodName] = methodMeta
					} else {
						// Even if type is not in this file, we might want to collect it?
						// But for now let's stick to what's requested.
						// We can create a placeholder if needed.
						richAST.Types[fullBaseType] = &transpiler.TypeMetadata{
							Name:    baseType,
							Package: pkgName,
							Methods: map[string]*transpiler.MethodMetadata{methodName: methodMeta},
							Fields:  make(map[string]transpiler.Type),
						}
					}
					// Record the declaration so the sibling pass can detect a
					// second definition of this method in another file of the
					// package. This also catches a duplicate within this file
					// when the receiver type is declared in a sibling: the
					// check above sees only a placeholder entry whose
					// DefinedIn is still empty, and would let it through.
					if prev, dup := decls.recordMethod(fullBaseType+"."+methodName, filePath, methodMeta.Pos); dup {
						return methodRedefinedError(methodName, baseType, pkgName,
							ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
							prev.file, filePath, prev.pos)
					}
				}
			} else {
				// Top-level function
				funcName := ctx.Identifier().GetText()

				// Check for std library conflicts
				if err := CheckStdConflict(funcName, pkgName); err != nil {
					return err
				}

				fullFuncName := funcName
				if pkgName != "" && pkgName != "main" && pkgName != "test" {
					fullFuncName = pkgName + "." + funcName
				}
				funcMeta := &transpiler.FunctionMetadata{
					Name:      funcName,
					Package:   pkgName,
					Doc:       docAt(docs, ctx.GetStart()),
					Pos:       transpiler.PosFromToken(ctx.Identifier().GetStart()),
					DefinedIn: filePath,
				}
				// Collect type parameters first so we can resolve param types correctly
				funcMeta.TypeParams, funcMeta.TypeParamConstraints = a.declaredTypeParams(ctx.TypeParameters(), pkgName)
				funcMeta.ReturnType, funcMeta.GoResults = a.signatureResult(ctx.Signature(), pkgName, funcMeta.TypeParams)
				// Source spans of each default expression, keyed by param index.
				// Used to give the E0014 (default type mismatch) diagnostic an
				// exact caret span over the offending default value.
				var defaultSpans map[int]defaultExprSpan
				if ctx.Signature().Parameters() != nil {
					pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
					if pList := pCtx.ParameterList(); pList != nil {
						for i, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(galaparser.TypeText(paramCtx.Type_()), pkgName, funcMeta.TypeParams))
								funcMeta.Variadic = paramCtx.ELLIPSIS() != nil
							} else {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
							}
							// Extract parameter name
							if paramCtx.Identifier() != nil {
								funcMeta.ParamNames = append(funcMeta.ParamNames, paramCtx.Identifier().GetText())
							} else {
								funcMeta.ParamNames = append(funcMeta.ParamNames, "")
							}
							if exprCtx := recordParamDefault(&funcMeta.DefaultExprs, i, paramCtx); exprCtx != nil {
								if defaultSpans == nil {
									defaultSpans = make(map[int]defaultExprSpan)
								}
								defaultSpans[i] = spanOfCtx(exprCtx)
							}
						}
					}
				}
				// Validate default parameter rules once the sibling files' type
				// declarations are known too (section 2.5): a default is checked
				// against the type its parameter's named type is declared over.
				if len(funcMeta.DefaultExprs) > 0 {
					fd.pendingDefaultChecks = append(fd.pendingDefaultChecks, pendingDefaultCheck{
						meta: funcMeta, line: ctx.GetStart().GetLine(), col: ctx.GetStart().GetColumn(), spans: defaultSpans,
					})
				}
				// Reject redeclaration of a top-level function within the same
				// package. The sibling-metadata pass may have already registered
				// the function from another file; if it lives in a different
				// file, that's a true redeclaration (mirrors Go's "redeclared
				// in this block"). Skip when the existing entry is empty
				// (placeholder / cache) or refers to this same file
				// (re-analysis of the same source).
				if existing, ok := richAST.Functions[fullFuncName]; ok {
					if existing.DefinedIn != "" && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
						return funcRedeclaredError(funcName, pkgName,
							ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
							existing.DefinedIn, filePath, existing.Pos)
					}
				}
				// Record the declaration so the sibling pass can detect a
				// second definition of this function in another file of the
				// package.
				if prev, dup := decls.recordFunc(fullFuncName, filePath, funcMeta.Pos); dup {
					return funcRedeclaredError(funcName, pkgName,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						prev.file, filePath, prev.pos)
				}
				richAST.Functions[fullFuncName] = funcMeta
			}
		}
	}
	return nil
}

// collectFileEmbeds records the embed declarations sourceFile makes
// (analyze's section 2.75).
func collectFileEmbeds(sourceFile *grammar.SourceFileContext, richAST *transpiler.RichAST) {
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		ctx, ok := topDecl.EmbedDeclaration().(*grammar.EmbedDeclarationContext)
		if !ok || ctx == nil {
			continue
		}
		// The type name, if one is given.
		typeName := ""
		if ctx.Type_() != nil {
			typeName = galaparser.TypeText(ctx.Type_())
		}
		var patterns []string
		for _, s := range ctx.EmbedPatterns().(*grammar.EmbedPatternsContext).AllSTRING() {
			patterns = append(patterns, strings.Trim(s.GetText(), "\""))
		}
		richAST.EmbedDirectives = append(richAST.EmbedDirectives, embedDirective(ctx.Identifier().GetText(), typeName, patterns))
	}
}

// embedDirective is the directive of `embed val varName typeName = patterns`,
// typeName "" when the declaration gives none.
func embedDirective(varName, typeName string, patterns []string) transpiler.EmbedDirective {
	// Determine Go variable name
	goVar := varName
	if typeName == transpiler.TypeEmbeddedFS || typeName == "std.EmbeddedFS" {
		goVar = "_embed_" + varName
	} else if typeName == "" {
		// Infer type: glob patterns → EmbeddedFS, single file → string
		hasGlob := false
		for _, p := range patterns {
			if strings.ContainsAny(p, "*?") || len(patterns) > 1 {
				hasGlob = true
				break
			}
		}
		if hasGlob {
			typeName = transpiler.TypeEmbeddedFS
			goVar = "_embed_" + varName
		} else {
			typeName = "string"
		}
	}
	return transpiler.EmbedDirective{
		VarName:  varName,
		GoVar:    goVar,
		Patterns: patterns,
		TypeName: typeName,
	}
}

// markSiblingSealed sets DefinedIn, where it is empty, on the sealed type
// fullName that the sibling at absSibPath declares and on its cases'
// companions and their Apply.
func markSiblingSealed(richAST *transpiler.RichAST, pkgName, fullName, absSibPath string) {
	meta, ok := richAST.Types[fullName]
	if !ok {
		return
	}
	if meta.DefinedIn == "" {
		meta.DefinedIn = absSibPath
	}
	for _, v := range meta.SealedVariants {
		if cm, ok := richAST.Types[qualifiedName(pkgName, v.Name)]; ok && cm.DefinedIn == "" {
			cm.DefinedIn = absSibPath
			if apply := cm.Methods["Apply"]; apply != nil {
				apply.DefinedIn = absSibPath
			}
		}
	}
}

// sealedDeclaredHere marks the sealed type fullName, which the analyzed file
// declares, and its cases' companions as defined in that file, and queues the
// cases whose fields declare defaults: those are checked as a function's
// parameter defaults are, through the companion's Apply.
func (fd *fileDecls) sealedDeclaredHere(fullName string) {
	meta, ok := fd.richAST.Types[fullName]
	if !ok {
		return
	}
	meta.DefinedIn = fd.filePath
	for _, v := range meta.SealedVariants {
		cm, ok := fd.richAST.Types[qualifiedName(fd.pkgName, v.Name)]
		if !ok {
			continue
		}
		cm.DefinedIn = fd.filePath
		apply := cm.Methods["Apply"]
		if apply == nil {
			continue
		}
		apply.DefinedIn = fd.filePath // its field defaults are lowered as declared here
		if len(apply.DefaultExprs) == 0 {
			continue
		}
		// Each default's own position, so a mismatch underlines it.
		spans := make(map[int]defaultExprSpan, len(apply.DefaultExprs))
		for i, def := range apply.DefaultExprs {
			text := def.Text
			if nl := strings.IndexByte(text, '\n'); nl >= 0 {
				text = text[:nl]
			}
			spans[i] = defaultExprSpan{line: def.Pos.Line, startCol: def.Pos.Column, endCol: def.Pos.Column + len([]rune(text))}
		}
		fd.pendingDefaultChecks = append(fd.pendingDefaultChecks, pendingDefaultCheck{
			meta: &transpiler.FunctionMetadata{
				Name: v.Name, ParamTypes: apply.ParamTypes, ParamNames: apply.ParamNames, DefaultExprs: apply.DefaultExprs,
			},
			line: v.Pos.Line, col: v.Pos.Column,
			spans: spans,
		})
	}
}

// genericInterfaceMethodError is GALA-E0086 for the method spec methodName,
// at line:col, of the interface iface: the spec declares the type parameters
// methodTPs, whose constraints, as written, are methodCons. Go interface
// methods cannot have type parameters, and a call through an interface value
// is dispatched at run time, so no set of instantiations can be generated for
// it. The hint moves the parameters onto the interface, after its own; a
// name the interface already declares is listed once.
func genericInterfaceMethodError(line, col int, iface *transpiler.TypeMetadata, methodName string, methodTPs []string, methodCons map[string]string) error {
	typeName := iface.Name
	var decl []string
	seen := make(map[string]bool)
	add := func(tp, constraint string) {
		if !seen[tp] {
			seen[tp] = true
			decl = append(decl, tp+" "+constraint)
		}
	}
	for _, tp := range iface.TypeParams {
		add(tp, iface.TypeParamConstraints[tp])
	}
	for _, tp := range methodTPs {
		add(tp, methodCons[tp])
	}
	noun := "a type parameter"
	if len(methodTPs) > 1 {
		noun = "type parameters"
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeGenericInterfaceMethod,
		line, col,
		fmt.Sprintf("interface method %q of %q declares %s [%s]: a Go interface method cannot have type parameters", methodName, typeName, noun, strings.Join(methodTPs, ", ")),
		fmt.Sprintf("declare %s on the interface instead (type %s[%s] interface { ... }), or write %s as a generic function that takes a %s",
			strings.Join(methodTPs, ", "), typeName, strings.Join(decl, ", "), methodName, typeName),
	)
}
