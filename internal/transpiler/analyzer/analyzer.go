package analyzer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/module"
	"martianoff/gala/internal/transpiler/registry"
	"martianoff/gala/internal/transpiler/transformer"
)

// GetBaseMetadata loads standard library metadata for use in tests and backward compatibility.
// In normal compilation flow, std is loaded via implicit import in Analyze().
func GetBaseMetadata(p transpiler.GalaParser, searchPaths []string) *transpiler.RichAST {
	a := &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		resolver:     module.NewResolver(searchPaths),
	}

	stdAST, err := a.analyzePackage(registry.StdPackageName)
	if err != nil {
		// Return empty RichAST if std can't be loaded
		return &transpiler.RichAST{
			Types:            make(map[string]*transpiler.TypeMetadata),
			Functions:        make(map[string]*transpiler.FunctionMetadata),
			Packages:         make(map[string]string),
			CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
		}
	}
	return stdAST
}

// CheckStdConflict returns an error if the given name conflicts with std library exports.
// This prevents user code from shadowing std types and functions.
//
// This function delegates to the registry package which is the source of truth
// for prelude package exports.
func CheckStdConflict(name, pkgName string) error {
	return registry.CheckStdConflict(name, pkgName)
}

type galaAnalyzer struct {
	baseMetadata *transpiler.RichAST
	parser       transpiler.GalaParser
	searchPaths  []string
	packageFiles []string                       // Explicit sibling files belonging to the same package
	analyzedPkgs map[string]*transpiler.RichAST // Cache of analyzed packages
	checkedDirs  map[string]bool
	resolver     *module.Resolver // Handles module root discovery and package path resolution
}

// NewGalaAnalyzer creates a new transpiler.Analyzer implementation.
// It automatically finds the module root by looking for go.mod from the current working directory.
func NewGalaAnalyzer(p transpiler.GalaParser, searchPaths []string) transpiler.Analyzer {
	return &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		resolver:     module.NewResolver(searchPaths),
	}
}

// NewGalaAnalyzerWithBase creates a new transpiler.Analyzer with base metadata.
func NewGalaAnalyzerWithBase(base *transpiler.RichAST, p transpiler.GalaParser, searchPaths []string) transpiler.Analyzer {
	return &galaAnalyzer{
		baseMetadata: base,
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		resolver:     module.NewResolver(searchPaths),
	}
}

// NewGalaAnalyzerWithPackageFiles creates an analyzer that uses explicit package file list
// for sibling discovery instead of directory scanning. This enables full cross-file type
// resolution for main/test packages where directory scanning is too broad.
func NewGalaAnalyzerWithPackageFiles(p transpiler.GalaParser, searchPaths []string, packageFiles []string) transpiler.Analyzer {
	return &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		packageFiles: packageFiles,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		resolver:     module.NewResolver(searchPaths),
	}
}

// Analyze walk the ANTLR tree and collects metadata for RichAST.
func (a *galaAnalyzer) Analyze(tree antlr.Tree, filePath string) (*transpiler.RichAST, error) {
	sourceFile, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, fmt.Errorf("expected *grammar.SourceFileContext, got %T", tree)
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()

	var siblingTrees []*grammar.SourceFileContext
	if len(a.packageFiles) > 0 {
		// Explicit package files: parse each one, validate package name, add to siblings
		absFilePath, _ := filepath.Abs(filePath)
		for _, pf := range a.packageFiles {
			absPf, _ := filepath.Abs(pf)
			if absPf == absFilePath {
				continue // skip self
			}
			content, err := ioutil.ReadFile(pf)
			if err != nil {
				continue
			}
			tree, err := a.parser.Parse(string(content))
			if err != nil {
				continue
			}
			otherSF, ok := tree.(*grammar.SourceFileContext)
			if !ok {
				continue
			}
			otherPkgName := otherSF.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
			if otherPkgName != pkgName {
				return nil, fmt.Errorf("package file %s has package %s, expected %s", pf, otherPkgName, pkgName)
			}
			siblingTrees = append(siblingTrees, otherSF)
		}
	} else if filePath != "" {
		// Directory-discovered siblings (existing behavior)
		dirPath := filepath.Dir(filePath)
		absDirPath, err := filepath.Abs(dirPath)
		if err == nil && !a.checkedDirs[absDirPath] {
			a.checkedDirs[absDirPath] = true
			files, err := ioutil.ReadDir(dirPath)
			if err == nil {
				for _, f := range files {
					if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" {
						otherPath := filepath.Join(dirPath, f.Name())
						if otherPath == filePath {
							continue
						}
						content, err := ioutil.ReadFile(otherPath)
						if err != nil {
							continue
						}
						tree, err := a.parser.Parse(string(content))
						if err != nil {
							continue
						}
						otherSF, ok := tree.(*grammar.SourceFileContext)
						if !ok {
							continue
						}
						otherPkgName := otherSF.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
						// Allow _test.gala files to have different package names (like Go's _test.go convention)
						isTestFile := strings.HasSuffix(f.Name(), "_test.gala") || strings.HasSuffix(filePath, "_test.gala")
						if otherPkgName != pkgName && !isTestFile {
							return nil, fmt.Errorf("multiple package names in directory %s: %s and %s", dirPath, pkgName, otherPkgName)
						}
						if otherPkgName == pkgName && !isTestFile {
							siblingTrees = append(siblingTrees, otherSF)
						}
					}
				}
			}
		}
	}

	richAST := &transpiler.RichAST{
		Tree:             tree,
		PackageName:      pkgName,
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
	}

	// 0. Populate base metadata if provided (deprecated, for backward compatibility)
	if a.baseMetadata != nil {
		richAST.Merge(a.baseMetadata)
	}

	// 0.25 Load std package metadata
	// For non-std packages: add as implicit import
	// For std package: still load for intra-package type resolution, but don't add to Packages
	if cachedStd, ok := a.analyzedPkgs[registry.StdImportPath]; ok && cachedStd != nil {
		// Use cached std metadata
		richAST.Merge(cachedStd)
		if pkgName != registry.StdPackageName {
			richAST.Packages[registry.StdImportPath] = registry.StdPackageName
		}
	} else if _, inProgress := a.analyzedPkgs[registry.StdImportPath]; !inProgress {
		// First time analyzing std - set placeholder to prevent infinite recursion
		a.analyzedPkgs[registry.StdImportPath] = nil
		stdAST, err := a.analyzePackage(registry.StdPackageName)
		if err == nil {
			a.analyzedPkgs[registry.StdImportPath] = stdAST
			richAST.Merge(stdAST)
			if pkgName != registry.StdPackageName {
				richAST.Packages[registry.StdImportPath] = registry.StdPackageName
			}
		}
	}

	// 0.5 Scan imports
	for _, impDecl := range sourceFile.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			path := strings.Trim(s.STRING().GetText(), "\"")

			// Check if this is a GALA package (internal or external)
			isInternalGala := strings.HasPrefix(path, "martianoff/gala/")
			isExternalGala := a.resolver.IsGalaPackage(path)

			if isInternalGala || isExternalGala {
				// Determine how to resolve the package
				var relPath string
				if isInternalGala {
					relPath = strings.TrimPrefix(path, "martianoff/gala/")
				} else {
					relPath = path // External packages use full path
				}

				if cached, ok := a.analyzedPkgs[path]; ok && cached != nil {
					// Use cached metadata
					richAST.Merge(cached)
					if cached.PackageName != "" && cached.PackageName != "main" && cached.PackageName != "test" {
						richAST.Packages[path] = cached.PackageName
					}
				} else if _, inProgress := a.analyzedPkgs[path]; !inProgress {
					// First time analyzing this package - set placeholder to prevent infinite recursion
					a.analyzedPkgs[path] = nil

					// For external GALA packages, ensure they're transpiled
					if isExternalGala && !isInternalGala {
						if err := a.ensureTranspiled(path); err != nil {
							// Log error but continue - we'll still try to analyze
							fmt.Fprintf(os.Stderr, "Warning: failed to transpile dependency %s: %v\n", path, err)
						}
					}

					importedAST, err := a.analyzePackage(relPath)
					if err == nil {
						a.analyzedPkgs[path] = importedAST
						richAST.Merge(importedAST)
						// Store package name from the imported package
						if importedAST.PackageName != "" && importedAST.PackageName != "main" && importedAST.PackageName != "test" {
							richAST.Packages[path] = importedAST.PackageName
						} else {
							// Fallback if PackageName is not set properly
							for _, typeMeta := range importedAST.Types {
								if typeMeta.Package != "" && typeMeta.Package != "main" && typeMeta.Package != "test" && !registry.Global.IsPreludePackage(typeMeta.Package) {
									richAST.Packages[path] = typeMeta.Package
									break
								}
							}
						}
					}
				}
			}
		}
	}

	// 1. Collect all types
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if typeDecl := topDecl.TypeDeclaration(); typeDecl != nil {
			ctx := typeDecl.(*grammar.TypeDeclarationContext)
			typeName := ctx.Identifier().GetText()

			// Check for std library conflicts
			if err := CheckStdConflict(typeName, pkgName); err != nil {
				return nil, err
			}

			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}

			var meta *transpiler.TypeMetadata
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				meta = existing
				// Clear fields to avoid duplicates if re-analyzing
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				richAST.Types[fullTypeName] = meta
			}

			if ctx.TypeParameters() != nil {
				tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
				if tpList := tpCtx.TypeParameterList(); tpList != nil {
					for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
						tpCtx := tp.(*grammar.TypeParameterContext)
						tpId := tpCtx.Identifier(0)
						meta.TypeParams = append(meta.TypeParams, tpId.GetText())
						// Extract the constraint (second identifier in "T comparable")
						if len(tpCtx.AllIdentifier()) > 1 {
							constraint := tpCtx.Identifier(1).GetText()
							if meta.TypeParamConstraints == nil {
								meta.TypeParamConstraints = make(map[string]string)
							}
							meta.TypeParamConstraints[tpId.GetText()] = constraint
						}
					}
				}
			}

			if ctx.StructType() != nil {
				structType := ctx.StructType().(*grammar.StructTypeContext)
				for _, field := range structType.AllStructField() {
					fctx := field.(*grammar.StructFieldContext)
					fieldName := fctx.Identifier().GetText()
					meta.Fields[fieldName] = a.resolveTypeWithParams(fctx.Type_().GetText(), pkgName, meta.TypeParams)
					meta.FieldNames = append(meta.FieldNames, fieldName)
					meta.ImmutFlags = append(meta.ImmutFlags, fctx.VAR() == nil)
				}
			}

			// Extract interface method signatures as type methods
			if ctx.InterfaceType() != nil {
				ifaceType := ctx.InterfaceType().(*grammar.InterfaceTypeContext)
				for _, ms := range ifaceType.AllMethodSpec() {
					msCtx := ms.(*grammar.MethodSpecContext)
					methodName := msCtx.Identifier().GetText()
					methodMeta := &transpiler.MethodMetadata{
						Name:    methodName,
						Package: pkgName,
					}
					if msCtx.TypeParameters() != nil {
						tpCtx := msCtx.TypeParameters().(*grammar.TypeParametersContext)
						if tpList := tpCtx.TypeParameterList(); tpList != nil {
							for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
								tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
								methodMeta.TypeParams = append(methodMeta.TypeParams, tpId.GetText())
							}
						}
					}
					var allTypeParams []string
					allTypeParams = append(allTypeParams, meta.TypeParams...)
					allTypeParams = append(allTypeParams, methodMeta.TypeParams...)
					if msCtx.Signature().Type_() != nil {
						methodMeta.ReturnType = a.resolveTypeWithParams(msCtx.Signature().Type_().GetText(), pkgName, allTypeParams)
					}
					if msCtx.Signature().Parameters() != nil {
						pCtx := msCtx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
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

			// Check for std library conflicts
			if err := CheckStdConflict(typeName, pkgName); err != nil {
				return nil, err
			}

			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}

			var meta *transpiler.TypeMetadata
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				meta = existing
				// Clear fields to avoid duplicates if re-analyzing
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				richAST.Types[fullTypeName] = meta
			}

			if ctx.Parameters() != nil {
				paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
				if paramsCtx.ParameterList() != nil {
					for _, param := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
						pctx := param.(*grammar.ParameterContext)
						fieldName := pctx.Identifier().GetText()
						fieldType := ""
						if pctx.Type_() != nil {
							fieldType = pctx.Type_().GetText()
						}
						meta.Fields[fieldName] = a.resolveTypeWithParams(fieldType, pkgName, meta.TypeParams)
						meta.FieldNames = append(meta.FieldNames, fieldName)
						meta.ImmutFlags = append(meta.ImmutFlags, pctx.VAR() == nil)
					}
				}
			}
		}
	}

	// 1.5 Collect sealed types
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if sealedCtx := topDecl.SealedTypeDeclaration(); sealedCtx != nil {
			a.analyzeSealedType(sealedCtx.(*grammar.SealedTypeDeclarationContext), pkgName, richAST)
		}
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
						Name:    methodName,
						Package: pkgName,
					}
					if ctx.TypeParameters() != nil {
						tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
						if tpList := tpCtx.TypeParameterList(); tpList != nil {
							for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
								tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
								methodMeta.TypeParams = append(methodMeta.TypeParams, tpId.GetText())
							}
						}
					}

					// Collect receiver's type parameters to include when resolving types
					// e.g., for "func (s Some[T]) Unapply(o Option[T])", we need to know T is a type param
					var allTypeParams []string
					if typeMeta, ok := richAST.Types[fullBaseType]; ok {
						allTypeParams = append(allTypeParams, typeMeta.TypeParams...)
					}
					allTypeParams = append(allTypeParams, methodMeta.TypeParams...)

					if ctx.Signature().Type_() != nil {
						methodMeta.ReturnType = a.resolveTypeWithParams(ctx.Signature().Type_().GetText(), pkgName, allTypeParams)

						// Detect Go generics instantiation cycle:
						// If receiver is Container[T] and return is Container[SomeType[T, ...]]
						// Go would detect infinite type instantiation
						recvTypeStr := recvCtx.Type_().GetText()
						retTypeStr := ctx.Signature().Type_().GetText()
						if a.causesInstantiationCycle(recvTypeStr, retTypeStr) {
							methodMeta.IsGeneric = true
						}
					}

					if ctx.Signature().Parameters() != nil {
						pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
								} else {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
								}
							}
						}
					}

					if typeMeta, ok := richAST.Types[fullBaseType]; ok {
						if existing, exists := typeMeta.Methods[methodName]; exists {
							// Preserve IsGeneric if it was pre-populated
							methodMeta.IsGeneric = existing.IsGeneric
						}
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
				}
			} else {
				// Top-level function
				funcName := ctx.Identifier().GetText()

				// Check for std library conflicts
				if err := CheckStdConflict(funcName, pkgName); err != nil {
					return nil, err
				}

				fullFuncName := funcName
				if pkgName != "" && pkgName != "main" && pkgName != "test" {
					fullFuncName = pkgName + "." + funcName
				}
				funcMeta := &transpiler.FunctionMetadata{
					Name:    funcName,
					Package: pkgName,
				}
				// Collect type parameters first so we can resolve param types correctly
				if ctx.TypeParameters() != nil {
					tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
					if tpList := tpCtx.TypeParameterList(); tpList != nil {
						for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
							tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
							funcMeta.TypeParams = append(funcMeta.TypeParams, tpId.GetText())
						}
					}
				}
				if ctx.Signature().Type_() != nil {
					funcMeta.ReturnType = a.resolveTypeWithParams(ctx.Signature().Type_().GetText(), pkgName, funcMeta.TypeParams)
				}
				if ctx.Signature().Parameters() != nil {
					pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
					if pList := pCtx.ParameterList(); pList != nil {
						for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, funcMeta.TypeParams))
							} else {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
							}
						}
					}
				}
				richAST.Functions[fullFuncName] = funcMeta
			}
		}
	}

	// 2.5 Extract sibling file metadata.
	// When --package-files is provided, extract full metadata (structs, sealed types, methods)
	// to enable cross-file type resolution even in main/test packages.
	// When using directory scanning, only extract method/function signatures for non-main packages
	// to avoid interfering with isImmutableField and .Get() auto-unwrapping.
	if len(a.packageFiles) > 0 {
		// Explicit package files: full metadata extraction for ALL packages including main/test
		for _, sibTree := range siblingTrees {
			a.extractSiblingFullMetadata(sibTree, pkgName, richAST)
		}
	} else if pkgName != "main" && pkgName != "test" {
		// Directory-discovered siblings: lightweight extraction only (existing behavior)
		for _, sibTree := range siblingTrees {
			a.extractSiblingMethodSignatures(sibTree, pkgName, richAST)
		}
	}

	// 3. Discover companion objects - types with Unapply methods that can be used for pattern matching
	a.discoverCompanionObjects(richAST)

	return richAST, nil
}

// analyzeSealedType registers metadata for a sealed type declaration.
// It creates the parent type (with all variant fields merged + _variant),
// companion types for each case, and Apply/Unapply/IsXxx methods.
func (a *galaAnalyzer) analyzeSealedType(ctx *grammar.SealedTypeDeclarationContext, pkgName string, richAST *transpiler.RichAST) {
	typeName := ctx.Identifier().GetText()

	fullTypeName := typeName
	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		fullTypeName = pkgName + "." + typeName
	}

	// Collect type parameters from the sealed type
	var typeParams []string
	if ctx.TypeParameters() != nil {
		tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
		if tpList := tpCtx.TypeParameterList(); tpList != nil {
			for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
				tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
				typeParams = append(typeParams, tpId.GetText())
			}
		}
	}

	// Create parent type metadata with all variant fields merged + _variant
	parentMeta := &transpiler.TypeMetadata{
		Name:       typeName,
		Package:    pkgName,
		Methods:    make(map[string]*transpiler.MethodMetadata),
		Fields:     make(map[string]transpiler.Type),
		IsSealed:   true,
		TypeParams: typeParams,
	}

	// Process each case to collect fields (two passes: collect, then resolve conflicts)
	type variantFieldInfo struct {
		name     string
		typeName string
	}
	type variantInfo struct {
		name   string
		fields []variantFieldInfo
	}
	var variants []variantInfo

	// First pass: collect all variant fields
	allFieldTypes := make(map[string]map[string]bool) // field name -> set of type texts
	for _, caseCtx := range ctx.AllSealedCase() {
		sc := caseCtx.(*grammar.SealedCaseContext)
		variantName := sc.Identifier().GetText()
		vi := variantInfo{name: variantName}

		if sc.SealedCaseFieldList() != nil {
			fieldList := sc.SealedCaseFieldList().(*grammar.SealedCaseFieldListContext)
			for _, fieldCtx := range fieldList.AllSealedCaseField() {
				fc := fieldCtx.(*grammar.SealedCaseFieldContext)
				fieldName := fc.Identifier().GetText()
				fieldTypeStr := fc.Type_().GetText()
				vi.fields = append(vi.fields, variantFieldInfo{fieldName, fieldTypeStr})
				if allFieldTypes[fieldName] == nil {
					allFieldTypes[fieldName] = make(map[string]bool)
				}
				allFieldTypes[fieldName][fieldTypeStr] = true
			}
		}

		variants = append(variants, vi)
	}

	// Detect field name conflicts: same name with different types requires prefixing
	conflictingFields := make(map[string]bool)
	for fieldName, typeSet := range allFieldTypes {
		if len(typeSet) > 1 {
			conflictingFields[fieldName] = true
		}
	}

	// Second pass: register fields with correct struct field names (prefixed if conflicting)
	addedFields := make(map[string]bool)
	for _, vi := range variants {
		for _, f := range vi.fields {
			structFieldName := f.name
			if conflictingFields[f.name] {
				structFieldName = vi.name + f.name // e.g., "AddLeft", "SubLeft"
			}

			if addedFields[structFieldName] {
				continue // shared field already added
			}
			addedFields[structFieldName] = true

			parentMeta.Fields[structFieldName] = a.resolveTypeWithParams(f.typeName, pkgName, typeParams)
			parentMeta.FieldNames = append(parentMeta.FieldNames, structFieldName)
			// Self-referential fields use pointer indirection (not Immutable-wrapped)
			isRecursive := f.typeName == typeName || strings.HasPrefix(f.typeName, typeName+"[")
			parentMeta.ImmutFlags = append(parentMeta.ImmutFlags, !isRecursive)
		}
	}

	// Add _variant field
	parentMeta.Fields["_variant"] = transpiler.BasicType{Name: "uint8"}
	parentMeta.FieldNames = append(parentMeta.FieldNames, "_variant")
	parentMeta.ImmutFlags = append(parentMeta.ImmutFlags, true)

	// Store variant metadata on parent
	for _, vi := range variants {
		sv := transpiler.SealedVariant{Name: vi.name}
		for _, f := range vi.fields {
			sv.FieldNames = append(sv.FieldNames, f.name)
			sv.FieldTypes = append(sv.FieldTypes, a.resolveTypeWithParams(f.typeName, pkgName, typeParams))
		}
		parentMeta.SealedVariants = append(parentMeta.SealedVariants, sv)
	}

	richAST.Types[fullTypeName] = parentMeta

	// For each variant, create companion type and register methods
	for _, vi := range variants {
		companionName := vi.name
		fullCompanionName := companionName
		if pkgName != "" && pkgName != "main" && pkgName != "test" {
			fullCompanionName = pkgName + "." + companionName
		}

		companionMeta := &transpiler.TypeMetadata{
			Name:       companionName,
			Package:    pkgName,
			Methods:    make(map[string]*transpiler.MethodMetadata),
			Fields:     make(map[string]transpiler.Type),
			TypeParams: typeParams,
		}

		// Apply method: takes the variant's fields, returns parent type
		applyMeta := &transpiler.MethodMetadata{
			Name:    "Apply",
			Package: pkgName,
		}
		for _, f := range vi.fields {
			applyMeta.ParamTypes = append(applyMeta.ParamTypes, a.resolveTypeWithParams(f.typeName, pkgName, typeParams))
		}
		// Return type is the parent sealed type
		if len(typeParams) > 0 {
			baseType := transpiler.NamedType{Package: pkgName, Name: typeName}
			var params []transpiler.Type
			for _, tp := range typeParams {
				params = append(params, transpiler.BasicType{Name: tp})
			}
			applyMeta.ReturnType = transpiler.GenericType{Base: baseType, Params: params}
		} else {
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				applyMeta.ReturnType = transpiler.NamedType{Package: pkgName, Name: typeName}
			} else {
				applyMeta.ReturnType = transpiler.BasicType{Name: typeName}
			}
		}
		companionMeta.Methods["Apply"] = applyMeta

		// Unapply method
		unapplyMeta := &transpiler.MethodMetadata{
			Name:    "Unapply",
			Package: pkgName,
		}
		// Param: the parent type
		unapplyMeta.ParamTypes = append(unapplyMeta.ParamTypes, applyMeta.ReturnType)
		// Return type depends on number of fields
		switch len(vi.fields) {
		case 0:
			unapplyMeta.ReturnType = transpiler.BasicType{Name: "bool"}
		case 1:
			fieldType := a.resolveTypeWithParams(vi.fields[0].typeName, pkgName, typeParams)
			unapplyMeta.ReturnType = transpiler.GenericType{
				Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: "Option"},
				Params: []transpiler.Type{fieldType},
			}
		default:
			// Option[Tuple[...]]
			var tupleParams []transpiler.Type
			for _, f := range vi.fields {
				tupleParams = append(tupleParams, a.resolveTypeWithParams(f.typeName, pkgName, typeParams))
			}
			tupleName := fmt.Sprintf("Tuple%d", len(vi.fields))
			if len(vi.fields) == 2 {
				tupleName = "Tuple"
			}
			tupleType := transpiler.GenericType{
				Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: tupleName},
				Params: tupleParams,
			}
			unapplyMeta.ReturnType = transpiler.GenericType{
				Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: "Option"},
				Params: []transpiler.Type{tupleType},
			}
		}
		companionMeta.Methods["Unapply"] = unapplyMeta

		richAST.Types[fullCompanionName] = companionMeta
	}

	// Register isXxx() methods on parent type (private)
	for _, vi := range variants {
		isMethodName := "is" + vi.name
		parentMeta.Methods[isMethodName] = &transpiler.MethodMetadata{
			Name:       isMethodName,
			Package:    pkgName,
			ReturnType: transpiler.BasicType{Name: "bool"},
		}
	}
}

// discoverCompanionObjects identifies types that can be used as pattern extractors.
// A companion object is a type that has an Unapply method and optionally an Apply method.
// From the Apply method, we can determine what container type it works with and which
// type parameter indices are extracted.
func (a *galaAnalyzer) discoverCompanionObjects(richAST *transpiler.RichAST) {
	for typeName, meta := range richAST.Types {
		// Check if this type has an Unapply method
		if _, hasUnapply := meta.Methods["Unapply"]; !hasUnapply {
			continue
		}

		// Check if this type has an Apply method
		applyMethod, hasApply := meta.Methods["Apply"]
		if !hasApply {
			continue
		}

		// Get the return type of Apply to determine the target container type
		if applyMethod.ReturnType == nil || applyMethod.ReturnType.IsNil() {
			continue
		}

		// Parse the return type to get the container type and its type parameters
		returnType := applyMethod.ReturnType
		var targetType string
		var containerTypeParams []string

		switch rt := returnType.(type) {
		case transpiler.GenericType:
			targetType = rt.Base.BaseName()
			for _, param := range rt.Params {
				containerTypeParams = append(containerTypeParams, param.String())
			}
		case transpiler.BasicType:
			targetType = rt.Name
		case transpiler.NamedType:
			targetType = rt.Name
		default:
			continue
		}

		// Determine which indices are extracted based on Apply method parameters
		// The Apply method's parameter types tell us which container type params are extracted
		extractIndices := a.computeExtractIndices(applyMethod, containerTypeParams)

		companionMeta := &transpiler.CompanionObjectMetadata{
			Name:           meta.Name,
			Package:        meta.Package,
			TargetType:     targetType,
			ExtractIndices: extractIndices,
		}

		// Store with both short and full name for lookup
		richAST.CompanionObjects[meta.Name] = companionMeta
		if meta.Package != "" && meta.Package != "main" && meta.Package != "test" {
			richAST.CompanionObjects[typeName] = companionMeta
		}
	}
}

// computeExtractIndices determines which type parameter indices are extracted by a companion object.
// It looks at the Apply method's parameters and finds their positions in the container's type parameters.
func (a *galaAnalyzer) computeExtractIndices(applyMethod *transpiler.MethodMetadata, containerTypeParams []string) []int {
	var indices []int

	// For each parameter type in Apply, find its index in the container's type parameters
	for _, paramType := range applyMethod.ParamTypes {
		if paramType == nil || paramType.IsNil() {
			continue
		}
		paramTypeName := normalizeTypeName(paramType.String())

		// Find this type in the container's type parameters
		for idx, containerParam := range containerTypeParams {
			normalizedContainerParam := normalizeTypeName(containerParam)
			if normalizedContainerParam == paramTypeName {
				indices = append(indices, idx)
				break
			}
		}
	}

	// If we couldn't determine indices from parameters, default to [0]
	// This handles cases like None which has no parameters
	if len(indices) == 0 && len(containerTypeParams) > 0 {
		// For extractors with no params (like None), don't add any indices
		// They match but don't extract values
	}

	return indices
}

// normalizeTypeName removes package prefixes for comparison purposes.
func normalizeTypeName(name string) string {
	// Remove common package prefixes
	if strings.HasPrefix(name, "std.") {
		return name[4:]
	}
	return name
}

// causesInstantiationCycle checks if a method return type would cause a Go generics
// instantiation cycle. This happens when:
// - The receiver is a generic type (e.g., MyList[T])
// - The return type is the same base type (e.g., MyList)
// - But with different type arguments (e.g., MyList[Pair[T, int]])
// Go's compiler detects this as a potential infinite instantiation chain.
func (a *galaAnalyzer) causesInstantiationCycle(recvTypeStr, retTypeStr string) bool {
	// Extract base type and type args from receiver
	recvBase, recvArgs := extractBaseAndArgs(recvTypeStr)
	if recvBase == "" || len(recvArgs) == 0 {
		return false // Not a generic receiver
	}

	// Extract base type and type args from return type
	retBase, retArgs := extractBaseAndArgs(retTypeStr)
	if retBase == "" {
		return false
	}

	// Check if base types match
	if recvBase != retBase {
		return false
	}

	// Check if type arguments differ
	// If they're exactly the same, no cycle (e.g., MyList[T] -> MyList[T])
	// If they differ, potential cycle (e.g., MyList[T] -> MyList[Pair[T, int]])
	if len(recvArgs) != len(retArgs) {
		return true // Different number of args = different
	}

	for i, recvArg := range recvArgs {
		if recvArg != retArgs[i] {
			return true // Different arg = potential cycle
		}
	}

	return false
}

// extractBaseAndArgs extracts the base type name and type arguments from a type string.
// For example, "MyList[T]" returns ("MyList", ["T"])
// "MyList[Pair[T, int]]" returns ("MyList", ["Pair[T, int]"])
func extractBaseAndArgs(typeStr string) (string, []string) {
	// Find the first '[' to separate base from args
	bracketIdx := strings.Index(typeStr, "[")
	if bracketIdx == -1 {
		return typeStr, nil
	}

	base := typeStr[:bracketIdx]
	argsStr := typeStr[bracketIdx+1 : len(typeStr)-1] // Remove outer brackets

	// Parse the type arguments, handling nested brackets
	var args []string
	depth := 0
	start := 0
	for i, ch := range argsStr {
		switch ch {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				arg := strings.TrimSpace(argsStr[start:i])
				if arg != "" {
					args = append(args, arg)
				}
				start = i + 1
			}
		}
	}
	// Add the last argument
	lastArg := strings.TrimSpace(argsStr[start:])
	if lastArg != "" {
		args = append(args, lastArg)
	}

	return base, args
}

func (a *galaAnalyzer) resolveType(typeName string, pkgName string) transpiler.Type {
	return a.resolveTypeWithParams(typeName, pkgName, nil)
}

// resolveTypeWithParams resolves a type name, taking into account type parameters
// that should not be prefixed with the package name.
func (a *galaAnalyzer) resolveTypeWithParams(typeName string, pkgName string, typeParams []string) transpiler.Type {
	if typeName == "" {
		return transpiler.NilType{}
	}

	// Handle function types: func(params) results
	if strings.HasPrefix(typeName, "func(") {
		return a.resolveFuncType(typeName, pkgName, typeParams)
	}

	// Handle array/slice types: []T - recursively resolve element type
	if strings.HasPrefix(typeName, "[]") {
		elemType := a.resolveTypeWithParams(typeName[2:], pkgName, typeParams)
		return transpiler.ArrayType{Elem: elemType}
	}

	// Handle pointer types: *T - recursively resolve element type
	if strings.HasPrefix(typeName, "*") {
		elemType := a.resolveTypeWithParams(typeName[1:], pkgName, typeParams)
		return transpiler.PointerType{Elem: elemType}
	}

	// If it's already package-qualified, just parse it
	if strings.Contains(typeName, ".") {
		return transpiler.ParseType(typeName)
	}

	// Check if it's a type parameter - these should not be prefixed
	for _, tp := range typeParams {
		if typeName == tp {
			return transpiler.BasicType{Name: typeName}
		}
	}

	// Check if it's a builtin/primitive type - these should never be package-qualified
	if transpiler.IsPrimitiveType(typeName) {
		return transpiler.ParseType(typeName)
	}

	// Handle generic types: Option[T], Tuple[A, B] - recursively resolve type args
	if idx := strings.Index(typeName, "["); idx != -1 {
		baseTypeName := typeName[:idx]
		argsStr := typeName[idx+1 : len(typeName)-1]

		// Resolve base type (may need std prefix)
		var baseType transpiler.Type
		if a.isStdType(baseTypeName) {
			baseType = transpiler.NamedType{Package: registry.StdPackageName, Name: baseTypeName}
		} else if pkgName != "" && pkgName != "main" && pkgName != "test" {
			baseType = transpiler.NamedType{Package: pkgName, Name: baseTypeName}
		} else {
			baseType = transpiler.BasicType{Name: baseTypeName}
		}

		// Parse and recursively resolve type arguments
		_, argStrs := extractBaseAndArgs(typeName)
		var params []transpiler.Type
		for _, argStr := range argStrs {
			params = append(params, a.resolveTypeWithParams(strings.TrimSpace(argStr), pkgName, typeParams))
		}

		// Only wrap in GenericType if there are params
		if len(params) > 0 {
			return transpiler.GenericType{Base: baseType, Params: params}
		}
		// Fall through to handle base type without params (shouldn't happen)
		_ = argsStr // silence unused variable warning
	}

	// Check if it's a known std type (non-generic)
	if a.isStdType(typeName) {
		return transpiler.NamedType{Package: registry.StdPackageName, Name: typeName}
	}

	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		return transpiler.NamedType{Package: pkgName, Name: typeName}
	}
	return transpiler.BasicType{Name: typeName}
}

// resolveFuncType resolves a function type string like "func(T) Option[U]"
func (a *galaAnalyzer) resolveFuncType(typeName string, pkgName string, typeParams []string) transpiler.Type {
	// Find the matching closing parenthesis for the parameters
	openParen := strings.Index(typeName, "(")
	if openParen == -1 {
		return transpiler.ParseType(typeName)
	}

	parenCount := 0
	closeParen := -1
	for i := openParen; i < len(typeName); i++ {
		switch typeName[i] {
		case '(':
			parenCount++
		case ')':
			parenCount--
			if parenCount == 0 {
				closeParen = i
				break
			}
		}
		if closeParen != -1 {
			break
		}
	}

	if closeParen == -1 {
		return transpiler.ParseType(typeName)
	}

	paramsStr := typeName[openParen+1 : closeParen]
	resultStr := strings.TrimSpace(typeName[closeParen+1:])

	// Parse parameters
	var params []transpiler.Type
	if paramsStr != "" {
		paramStrs := a.splitTypeList(paramsStr)
		for _, p := range paramStrs {
			params = append(params, a.resolveTypeWithParams(strings.TrimSpace(p), pkgName, typeParams))
		}
	}

	// Parse results
	var results []transpiler.Type
	if resultStr != "" {
		// Handle tuple results like (int, string)
		if strings.HasPrefix(resultStr, "(") && strings.HasSuffix(resultStr, ")") {
			resultStrs := a.splitTypeList(resultStr[1 : len(resultStr)-1])
			for _, r := range resultStrs {
				results = append(results, a.resolveTypeWithParams(strings.TrimSpace(r), pkgName, typeParams))
			}
		} else {
			results = append(results, a.resolveTypeWithParams(resultStr, pkgName, typeParams))
		}
	}

	return transpiler.FuncType{Params: params, Results: results}
}

// splitTypeList splits a comma-separated type list, respecting brackets
func (a *galaAnalyzer) splitTypeList(s string) []string {
	var result []string
	bracketCount := 0
	parenCount := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			bracketCount++
		case ']':
			bracketCount--
		case '(':
			parenCount++
		case ')':
			parenCount--
		case ',':
			if bracketCount == 0 && parenCount == 0 {
				result = append(result, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// isStdType checks if a type name is a known std library type
func (a *galaAnalyzer) isStdType(name string) bool {
	return registry.IsStdType(name)
}

func (a *galaAnalyzer) analyzePackage(relPath string) (*transpiler.RichAST, error) {
	// Use the resolver to find the package directory
	dirPath, err := a.resolver.ResolvePackagePath(relPath)
	if err != nil {
		return nil, fmt.Errorf("package not found: %s", relPath)
	}

	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	pkgAST := &transpiler.RichAST{
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
	}

	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" {
			filePath := filepath.Join(dirPath, f.Name())
			content, err := ioutil.ReadFile(filePath)
			if err != nil {
				continue
			}
			tree, err := a.parser.Parse(string(content))
			if err != nil {
				continue
			}
			res, err := a.Analyze(tree, filePath)
			if err == nil {
				if pkgAST.PackageName == "" {
					pkgAST.PackageName = res.PackageName
				} else if pkgAST.PackageName != res.PackageName {
					// Allow _test.gala files to have different package names (like Go's _test.go convention)
					// Skip merging them into package AST since they're external tests
					if strings.HasSuffix(f.Name(), "_test.gala") {
						continue
					}
					return nil, fmt.Errorf("multiple package names in directory %s: %s and %s", dirPath, pkgAST.PackageName, res.PackageName)
				}
				pkgAST.Merge(res)
			}
		}
	}

	// For Go-only packages (no .gala files), scan .go files for exported symbols.
	// This enables the transpiler to warn when two dot-imported packages export the same symbol.
	hasGalaFiles := false
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" {
			hasGalaFiles = true
			break
		}
	}
	if !hasGalaFiles {
		a.extractGoFileExports(files, dirPath, relPath, pkgAST)
	}

	return pkgAST, nil
}

// goExportedFuncRe matches exported (capitalized) standalone function declarations in Go files.
// Only matches top-level functions, not methods (which have a receiver before the name).
var goExportedFuncRe = regexp.MustCompile(`(?m)^func\s+([A-Z]\w*)\s*[\[(]`)

// goExportedTypeRe matches exported type declarations in Go files.
var goExportedTypeRe = regexp.MustCompile(`(?m)^type\s+([A-Z]\w*)\s+`)

// goPkgNameRe matches the package declaration in Go files.
var goPkgNameRe = regexp.MustCompile(`(?m)^package\s+(\w+)`)

// extractGoFileExports scans .go files in a directory for exported symbol names.
// These are stored in pkgAST.GoExports (separate from Types/Functions to avoid
// interfering with type resolution). Used for dot-import clash detection.
func (a *galaAnalyzer) extractGoFileExports(files []os.FileInfo, dirPath, relPath string, pkgAST *transpiler.RichAST) {
	var symbols []string
	seen := make(map[string]bool)

	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".go" || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		content, err := ioutil.ReadFile(filepath.Join(dirPath, f.Name()))
		if err != nil {
			continue
		}
		src := string(content)

		// Extract package name if not already set
		if pkgAST.PackageName == "" {
			if m := goPkgNameRe.FindStringSubmatch(src); len(m) > 1 {
				pkgAST.PackageName = m[1]
			}
		}

		// Extract exported function names
		for _, m := range goExportedFuncRe.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				symbols = append(symbols, m[1])
			}
		}

		// Extract exported type names
		for _, m := range goExportedTypeRe.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				symbols = append(symbols, m[1])
			}
		}
	}

	if len(symbols) > 0 {
		pkg := pkgAST.PackageName
		if pkg == "" {
			pkg = relPath // fallback
		}
		if pkgAST.GoExports == nil {
			pkgAST.GoExports = make(map[string][]string)
		}
		pkgAST.GoExports[pkg] = symbols
	}
}

// ensureTranspiled checks if an external GALA package has been transpiled
// and transpiles it if necessary. The transpiled .go files are written
// to the same cache directory as the .gala source files.
func (a *galaAnalyzer) ensureTranspiled(importPath string) error {
	// Find the package directory in the cache
	dirPath, err := a.resolver.ResolvePackagePath(importPath)
	if err != nil {
		return err
	}

	// Check if any .go files already exist (indicating transpilation was done)
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return err
	}

	hasGoFiles := false
	var galaFiles []string
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := filepath.Ext(f.Name())
		if ext == ".go" && !strings.HasSuffix(f.Name(), "_test.go") {
			hasGoFiles = true
			break
		}
		if ext == ".gala" && !strings.HasSuffix(f.Name(), "_test.gala") {
			galaFiles = append(galaFiles, f.Name())
		}
	}

	// If already transpiled, nothing to do
	if hasGoFiles {
		return nil
	}

	// Transpile each .gala file
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

	for _, galaFile := range galaFiles {
		srcPath := filepath.Join(dirPath, galaFile)
		content, err := ioutil.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", srcPath, err)
		}

		// Parse the file
		tree, err := a.parser.Parse(string(content))
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", srcPath, err)
		}

		// Analyze without recursion by using a separate analyzer
		// This avoids circular dependency issues
		tempAnalyzer := &galaAnalyzer{
			parser:       a.parser,
			searchPaths:  a.searchPaths,
			analyzedPkgs: make(map[string]*transpiler.RichAST),
			checkedDirs:  make(map[string]bool),
			resolver:     a.resolver,
		}

		richAST, err := tempAnalyzer.Analyze(tree, srcPath)
		if err != nil {
			return fmt.Errorf("failed to analyze %s: %w", srcPath, err)
		}

		// Transform to Go AST
		fset, goAST, err := tr.Transform(richAST)
		if err != nil {
			return fmt.Errorf("failed to transform %s: %w", srcPath, err)
		}

		// Generate Go code
		goCode, err := g.Generate(fset, goAST)
		if err != nil {
			return fmt.Errorf("failed to generate Go code for %s: %w", srcPath, err)
		}

		// Write the Go file
		goFileName := strings.TrimSuffix(galaFile, ".gala") + ".gen.go"
		goPath := filepath.Join(dirPath, goFileName)
		if err := os.WriteFile(goPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", goPath, err)
		}
	}

	return nil
}

func getBaseTypeName(ctx grammar.ITypeContext) string {
	if ctx == nil {
		return ""
	}
	if ctx.QualifiedIdentifier() != nil {
		// Get the full qualified name (e.g., "std.Option" or just "Option")
		return ctx.QualifiedIdentifier().GetText()
	}
	if strings.HasPrefix(ctx.GetText(), "[]") && len(ctx.AllType_()) > 0 {
		return "[]" + getBaseTypeName(ctx.Type_(0))
	}
	if len(ctx.AllType_()) > 0 {
		// Handles pointers (*T) and potentially other nested types
		return getBaseTypeName(ctx.Type_(0))
	}
	return ""
}

// extractSiblingFullMetadata extracts full type metadata from a sibling .gala file.
// Unlike extractSiblingMethodSignatures, this includes struct fields, sealed types,
// shorthand structs, and all method/function signatures. Used when --package-files
// is provided to enable full cross-file type resolution.
func (a *galaAnalyzer) extractSiblingFullMetadata(sibTree *grammar.SourceFileContext, pkgName string, richAST *transpiler.RichAST) {
	// 1. Collect struct types with full field info
	for _, topDecl := range sibTree.AllTopLevelDeclaration() {
		if typeDecl := topDecl.TypeDeclaration(); typeDecl != nil {
			ctx := typeDecl.(*grammar.TypeDeclarationContext)
			typeName := ctx.Identifier().GetText()
			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}
			// Skip if type already has field info (main file takes precedence).
			// But allow overriding placeholder entries (created by method collection
			// when a method references a type from a sibling file).
			if existing, ok := richAST.Types[fullTypeName]; ok && len(existing.FieldNames) > 0 {
				continue
			}
			// Preserve existing methods from placeholder entry (e.g., methods from current file)
			existingMethods := make(map[string]*transpiler.MethodMetadata)
			if existing, ok := richAST.Types[fullTypeName]; ok {
				for k, v := range existing.Methods {
					existingMethods[k] = v
				}
			}
			meta := &transpiler.TypeMetadata{
				Name:    typeName,
				Package: pkgName,
				Methods: existingMethods,
				Fields:  make(map[string]transpiler.Type),
			}
			if ctx.TypeParameters() != nil {
				tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
				if tpList := tpCtx.TypeParameterList(); tpList != nil {
					for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
						tpCtx := tp.(*grammar.TypeParameterContext)
						tpId := tpCtx.Identifier(0)
						meta.TypeParams = append(meta.TypeParams, tpId.GetText())
						if len(tpCtx.AllIdentifier()) > 1 {
							constraint := tpCtx.Identifier(1).GetText()
							if meta.TypeParamConstraints == nil {
								meta.TypeParamConstraints = make(map[string]string)
							}
							meta.TypeParamConstraints[tpId.GetText()] = constraint
						}
					}
				}
			}
			if ctx.StructType() != nil {
				structType := ctx.StructType().(*grammar.StructTypeContext)
				for _, field := range structType.AllStructField() {
					fctx := field.(*grammar.StructFieldContext)
					fieldName := fctx.Identifier().GetText()
					meta.Fields[fieldName] = a.resolveTypeWithParams(fctx.Type_().GetText(), pkgName, meta.TypeParams)
					meta.FieldNames = append(meta.FieldNames, fieldName)
					meta.ImmutFlags = append(meta.ImmutFlags, fctx.VAR() == nil)
				}
			}
			if ctx.InterfaceType() != nil {
				ifaceType := ctx.InterfaceType().(*grammar.InterfaceTypeContext)
				for _, ms := range ifaceType.AllMethodSpec() {
					msCtx := ms.(*grammar.MethodSpecContext)
					methodName := msCtx.Identifier().GetText()
					methodMeta := &transpiler.MethodMetadata{
						Name:    methodName,
						Package: pkgName,
					}
					if msCtx.TypeParameters() != nil {
						tpCtx := msCtx.TypeParameters().(*grammar.TypeParametersContext)
						if tpList := tpCtx.TypeParameterList(); tpList != nil {
							for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
								tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
								methodMeta.TypeParams = append(methodMeta.TypeParams, tpId.GetText())
							}
						}
					}
					var allTypeParams []string
					allTypeParams = append(allTypeParams, meta.TypeParams...)
					allTypeParams = append(allTypeParams, methodMeta.TypeParams...)
					if msCtx.Signature().Type_() != nil {
						methodMeta.ReturnType = a.resolveTypeWithParams(msCtx.Signature().Type_().GetText(), pkgName, allTypeParams)
					}
					if msCtx.Signature().Parameters() != nil {
						pCtx := msCtx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
								} else {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
								}
							}
						}
					}
					meta.Methods[methodName] = methodMeta
				}
			}
			richAST.Types[fullTypeName] = meta
		}

		// Shorthand struct declarations
		if shorthandCtx := topDecl.StructShorthandDeclaration(); shorthandCtx != nil {
			ctx := shorthandCtx.(*grammar.StructShorthandDeclarationContext)
			typeName := ctx.Identifier().GetText()
			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}
			if existing, ok := richAST.Types[fullTypeName]; ok && len(existing.FieldNames) > 0 {
				continue
			}
			existingMethods := make(map[string]*transpiler.MethodMetadata)
			if existing, ok := richAST.Types[fullTypeName]; ok {
				for k, v := range existing.Methods {
					existingMethods[k] = v
				}
			}
			meta := &transpiler.TypeMetadata{
				Name:    typeName,
				Package: pkgName,
				Methods: existingMethods,
				Fields:  make(map[string]transpiler.Type),
			}
			if ctx.Parameters() != nil {
				paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
				if paramsCtx.ParameterList() != nil {
					for _, param := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
						pctx := param.(*grammar.ParameterContext)
						fieldName := pctx.Identifier().GetText()
						fieldType := ""
						if pctx.Type_() != nil {
							fieldType = pctx.Type_().GetText()
						}
						meta.Fields[fieldName] = a.resolveTypeWithParams(fieldType, pkgName, meta.TypeParams)
						meta.FieldNames = append(meta.FieldNames, fieldName)
						meta.ImmutFlags = append(meta.ImmutFlags, pctx.VAR() == nil)
					}
				}
			}
			richAST.Types[fullTypeName] = meta
		}
	}

	// 2. Collect sealed types
	for _, topDecl := range sibTree.AllTopLevelDeclaration() {
		if sealedCtx := topDecl.SealedTypeDeclaration(); sealedCtx != nil {
			ctx := sealedCtx.(*grammar.SealedTypeDeclarationContext)
			typeName := ctx.Identifier().GetText()
			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}
			// Skip if already has sealed variant info (main file takes precedence).
			// Allow overriding placeholders from method collection.
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.IsSealed {
				continue
			}
			a.analyzeSealedType(ctx, pkgName, richAST)
		}
	}

	// 3. Collect methods and functions (same as extractSiblingMethodSignatures second pass)
	for _, topDecl := range sibTree.AllTopLevelDeclaration() {
		if funcDeclCtx := topDecl.FunctionDeclaration(); funcDeclCtx != nil {
			ctx := funcDeclCtx.(*grammar.FunctionDeclarationContext)
			if ctx.Receiver() != nil {
				recvCtx := ctx.Receiver().(*grammar.ReceiverContext)
				baseType := getBaseTypeName(recvCtx.Type_())
				if baseType == "" {
					continue
				}
				methodName := ctx.Identifier().GetText()
				fullBaseType := baseType
				if pkgName != "" && pkgName != "main" && pkgName != "test" && !strings.Contains(baseType, ".") {
					fullBaseType = pkgName + "." + baseType
				}

				methodMeta := &transpiler.MethodMetadata{
					Name:    methodName,
					Package: pkgName,
				}
				if ctx.TypeParameters() != nil {
					tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
					if tpList := tpCtx.TypeParameterList(); tpList != nil {
						for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
							tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
							methodMeta.TypeParams = append(methodMeta.TypeParams, tpId.GetText())
						}
					}
				}

				var allTypeParams []string
				if typeMeta, ok := richAST.Types[fullBaseType]; ok {
					allTypeParams = append(allTypeParams, typeMeta.TypeParams...)
				}
				allTypeParams = append(allTypeParams, methodMeta.TypeParams...)

				if ctx.Signature().Type_() != nil {
					methodMeta.ReturnType = a.resolveTypeWithParams(ctx.Signature().Type_().GetText(), pkgName, allTypeParams)
				}
				if ctx.Signature().Parameters() != nil {
					pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
					if pList := pCtx.ParameterList(); pList != nil {
						for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
							} else {
								methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
							}
						}
					}
				}

				if typeMeta, ok := richAST.Types[fullBaseType]; ok {
					if _, exists := typeMeta.Methods[methodName]; !exists {
						typeMeta.Methods[methodName] = methodMeta
					}
				} else {
					richAST.Types[fullBaseType] = &transpiler.TypeMetadata{
						Name:    baseType,
						Package: pkgName,
						Methods: map[string]*transpiler.MethodMetadata{methodName: methodMeta},
						Fields:  make(map[string]transpiler.Type),
					}
				}
			} else {
				funcName := ctx.Identifier().GetText()
				fullFuncName := funcName
				if pkgName != "" && pkgName != "main" && pkgName != "test" {
					fullFuncName = pkgName + "." + funcName
				}
				if _, ok := richAST.Functions[fullFuncName]; !ok {
					funcMeta := &transpiler.FunctionMetadata{
						Name:    funcName,
						Package: pkgName,
					}
					if ctx.TypeParameters() != nil {
						tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
						if tpList := tpCtx.TypeParameterList(); tpList != nil {
							for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
								tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
								funcMeta.TypeParams = append(funcMeta.TypeParams, tpId.GetText())
							}
						}
					}
					if ctx.Signature().Type_() != nil {
						funcMeta.ReturnType = a.resolveTypeWithParams(ctx.Signature().Type_().GetText(), pkgName, funcMeta.TypeParams)
					}
					if ctx.Signature().Parameters() != nil {
						pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, funcMeta.TypeParams))
								} else {
									funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
								}
							}
						}
					}
					richAST.Functions[fullFuncName] = funcMeta
				}
			}
		}
	}
}

// extractSiblingMethodSignatures extracts method and function signatures from a sibling .gala file.
// It only populates method metadata (name, params, return type) and function metadata.
// It does NOT populate field information (FieldNames, ImmutFlags, Fields) to avoid
// interfering with isImmutableField and .Get() auto-unwrapping.
func (a *galaAnalyzer) extractSiblingMethodSignatures(sibTree *grammar.SourceFileContext, pkgName string, richAST *transpiler.RichAST) {
	// First pass: collect type declarations (for type parameter info)
	for _, topDecl := range sibTree.AllTopLevelDeclaration() {
		if typeDecl := topDecl.TypeDeclaration(); typeDecl != nil {
			ctx := typeDecl.(*grammar.TypeDeclarationContext)
			typeName := ctx.Identifier().GetText()
			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}
			// Only create type entry if not already present (from imports or current file)
			if _, ok := richAST.Types[fullTypeName]; !ok {
				meta := &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				if ctx.TypeParameters() != nil {
					tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
					if tpList := tpCtx.TypeParameterList(); tpList != nil {
						for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
							tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
							meta.TypeParams = append(meta.TypeParams, tpId.GetText())
						}
					}
				}
				richAST.Types[fullTypeName] = meta
			}
		}
	}

	// Second pass: collect method and function signatures
	for _, topDecl := range sibTree.AllTopLevelDeclaration() {
		if funcDeclCtx := topDecl.FunctionDeclaration(); funcDeclCtx != nil {
			ctx := funcDeclCtx.(*grammar.FunctionDeclarationContext)
			if ctx.Receiver() != nil {
				// Method declaration
				recvCtx := ctx.Receiver().(*grammar.ReceiverContext)
				baseType := getBaseTypeName(recvCtx.Type_())
				if baseType == "" {
					continue
				}
				methodName := ctx.Identifier().GetText()
				fullBaseType := baseType
				if pkgName != "" && pkgName != "main" && pkgName != "test" && !strings.Contains(baseType, ".") {
					fullBaseType = pkgName + "." + baseType
				}

				methodMeta := &transpiler.MethodMetadata{
					Name:    methodName,
					Package: pkgName,
				}
				if ctx.TypeParameters() != nil {
					tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
					if tpList := tpCtx.TypeParameterList(); tpList != nil {
						for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
							tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
							methodMeta.TypeParams = append(methodMeta.TypeParams, tpId.GetText())
						}
					}
				}

				var allTypeParams []string
				if typeMeta, ok := richAST.Types[fullBaseType]; ok {
					allTypeParams = append(allTypeParams, typeMeta.TypeParams...)
				}
				allTypeParams = append(allTypeParams, methodMeta.TypeParams...)

				if ctx.Signature().Type_() != nil {
					methodMeta.ReturnType = a.resolveTypeWithParams(ctx.Signature().Type_().GetText(), pkgName, allTypeParams)
				}
				if ctx.Signature().Parameters() != nil {
					pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
					if pList := pCtx.ParameterList(); pList != nil {
						for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
							} else {
								methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
							}
						}
					}
				}

				if typeMeta, ok := richAST.Types[fullBaseType]; ok {
					// Only add if not already present (don't override current file's methods)
					if _, exists := typeMeta.Methods[methodName]; !exists {
						typeMeta.Methods[methodName] = methodMeta
					}
				}
			} else {
				// Top-level function
				funcName := ctx.Identifier().GetText()
				fullFuncName := funcName
				if pkgName != "" && pkgName != "main" && pkgName != "test" {
					fullFuncName = pkgName + "." + funcName
				}
				// Only add if not already present
				if _, ok := richAST.Functions[fullFuncName]; !ok {
					funcMeta := &transpiler.FunctionMetadata{
						Name:    funcName,
						Package: pkgName,
					}
					if ctx.TypeParameters() != nil {
						tpCtx := ctx.TypeParameters().(*grammar.TypeParametersContext)
						if tpList := tpCtx.TypeParameterList(); tpList != nil {
							for _, tp := range tpList.(*grammar.TypeParameterListContext).AllTypeParameter() {
								tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
								funcMeta.TypeParams = append(funcMeta.TypeParams, tpId.GetText())
							}
						}
					}
					if ctx.Signature().Type_() != nil {
						funcMeta.ReturnType = a.resolveTypeWithParams(ctx.Signature().Type_().GetText(), pkgName, funcMeta.TypeParams)
					}
					if ctx.Signature().Parameters() != nil {
						pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, funcMeta.TypeParams))
								} else {
									funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
								}
							}
						}
					}
					richAST.Functions[fullFuncName] = funcMeta
				}
			}
		}
	}
}

var _ transpiler.Analyzer = (*galaAnalyzer)(nil)
