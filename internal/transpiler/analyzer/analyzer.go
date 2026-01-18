package analyzer

import (
	"fmt"
	"io/ioutil"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"os"
	"path/filepath"
	"strings"

	"github.com/antlr4-go/antlr/v4"
)

// GetBaseMetadata loads standard library metadata for use in tests and backward compatibility.
// In normal compilation flow, std is loaded via implicit import in Analyze().
func GetBaseMetadata(p transpiler.GalaParser, searchPaths []string) *transpiler.RichAST {
	// Try to find module root from current working directory
	cwd, _ := os.Getwd()
	moduleRoot, moduleName := findModuleRoot(cwd)

	// If not found from cwd, try from search paths
	if moduleRoot == "" {
		for _, sp := range searchPaths {
			absPath, err := filepath.Abs(sp)
			if err == nil {
				moduleRoot, moduleName = findModuleRoot(absPath)
				if moduleRoot != "" {
					break
				}
			}
		}
	}

	a := &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		moduleRoot:   moduleRoot,
		moduleName:   moduleName,
	}

	stdAST, err := a.analyzePackage(transpiler.StdPackage)
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
func CheckStdConflict(name, pkgName string) error {
	if pkgName == transpiler.StdPackage {
		return nil // std itself can define these
	}
	for _, stdType := range transpiler.StdExportedTypes {
		if name == stdType {
			return fmt.Errorf("type '%s' conflicts with std library export; choose a different name", name)
		}
	}
	for _, stdFunc := range transpiler.StdExportedFunctions {
		if name == stdFunc {
			return fmt.Errorf("function '%s' conflicts with std library export; choose a different name", name)
		}
	}
	return nil
}

type galaAnalyzer struct {
	baseMetadata *transpiler.RichAST
	parser       transpiler.GalaParser
	searchPaths  []string
	analyzedPkgs map[string]*transpiler.RichAST // Cache of analyzed packages
	checkedDirs  map[string]bool
	moduleRoot   string // Path to the module root (where go.mod is located)
	moduleName   string // Module name from go.mod (e.g., "martianoff/gala")
}

// findModuleRoot walks up from startPath looking for go.mod to determine the module root.
// Returns the module root path and module name, or empty strings if not found.
func findModuleRoot(startPath string) (string, string) {
	// Start from the given path
	dir := startPath
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	// Walk up looking for go.mod
	for {
		modPath := filepath.Join(dir, "go.mod")
		if content, err := ioutil.ReadFile(modPath); err == nil {
			// Parse module name from go.mod
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					moduleName := strings.TrimSpace(strings.TrimPrefix(line, "module "))
					return dir, moduleName
				}
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root, no go.mod found
			break
		}
		dir = parent
	}
	return "", ""
}

// NewGalaAnalyzer creates a new transpiler.Analyzer implementation.
// It automatically finds the module root by looking for go.mod from the current working directory.
func NewGalaAnalyzer(p transpiler.GalaParser, searchPaths []string) transpiler.Analyzer {
	// Try to find module root from current working directory
	cwd, _ := os.Getwd()
	moduleRoot, moduleName := findModuleRoot(cwd)

	// If not found from cwd, try from search paths
	if moduleRoot == "" {
		for _, sp := range searchPaths {
			absPath, err := filepath.Abs(sp)
			if err == nil {
				moduleRoot, moduleName = findModuleRoot(absPath)
				if moduleRoot != "" {
					break
				}
			}
		}
	}

	return &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		moduleRoot:   moduleRoot,
		moduleName:   moduleName,
	}
}

// NewGalaAnalyzerWithBase creates a new transpiler.Analyzer with base metadata.
func NewGalaAnalyzerWithBase(base *transpiler.RichAST, p transpiler.GalaParser, searchPaths []string) transpiler.Analyzer {
	// Try to find module root from current working directory
	cwd, _ := os.Getwd()
	moduleRoot, moduleName := findModuleRoot(cwd)

	// If not found from cwd, try from search paths
	if moduleRoot == "" {
		for _, sp := range searchPaths {
			absPath, err := filepath.Abs(sp)
			if err == nil {
				moduleRoot, moduleName = findModuleRoot(absPath)
				if moduleRoot != "" {
					break
				}
			}
		}
	}

	return &galaAnalyzer{
		baseMetadata: base,
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:  make(map[string]bool),
		moduleRoot:   moduleRoot,
		moduleName:   moduleName,
	}
}

// Analyze walk the ANTLR tree and collects metadata for RichAST.
func (a *galaAnalyzer) Analyze(tree antlr.Tree, filePath string) (*transpiler.RichAST, error) {
	sourceFile, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, fmt.Errorf("expected *grammar.SourceFileContext, got %T", tree)
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()

	if filePath != "" {
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
						if otherPkgName != pkgName {
							return nil, fmt.Errorf("multiple package names in directory %s: %s and %s", dirPath, pkgName, otherPkgName)
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
	if cachedStd, ok := a.analyzedPkgs[transpiler.StdImportPath]; ok && cachedStd != nil {
		// Use cached std metadata
		richAST.Merge(cachedStd)
		if pkgName != transpiler.StdPackage {
			richAST.Packages[transpiler.StdImportPath] = transpiler.StdPackage
		}
	} else if _, inProgress := a.analyzedPkgs[transpiler.StdImportPath]; !inProgress {
		// First time analyzing std - set placeholder to prevent infinite recursion
		a.analyzedPkgs[transpiler.StdImportPath] = nil
		stdAST, err := a.analyzePackage(transpiler.StdPackage)
		if err == nil {
			a.analyzedPkgs[transpiler.StdImportPath] = stdAST
			richAST.Merge(stdAST)
			if pkgName != transpiler.StdPackage {
				richAST.Packages[transpiler.StdImportPath] = transpiler.StdPackage
			}
		}
	}

	// 0.5 Scan imports
	for _, impDecl := range sourceFile.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			path := strings.Trim(s.STRING().GetText(), "\"")
			if strings.HasPrefix(path, "martianoff/gala/") {
				relPath := strings.TrimPrefix(path, "martianoff/gala/")
				if cached, ok := a.analyzedPkgs[path]; ok && cached != nil {
					// Use cached metadata
					richAST.Merge(cached)
					if cached.PackageName != "" && cached.PackageName != "main" && cached.PackageName != "test" {
						richAST.Packages[path] = cached.PackageName
					}
				} else if _, inProgress := a.analyzedPkgs[path]; !inProgress {
					// First time analyzing this package - set placeholder to prevent infinite recursion
					a.analyzedPkgs[path] = nil
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
								if typeMeta.Package != "" && typeMeta.Package != "main" && typeMeta.Package != "test" && typeMeta.Package != "std" {
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
						tpId := tp.(*grammar.TypeParameterContext).Identifier(0)
						meta.TypeParams = append(meta.TypeParams, tpId.GetText())
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

					if ctx.Signature().Type_() != nil {
						methodMeta.ReturnType = a.resolveType(ctx.Signature().Type_().GetText(), pkgName)
					}

					if ctx.Signature().Parameters() != nil {
						pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
						if pList := pCtx.ParameterList(); pList != nil {
							for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveType(paramCtx.Type_().GetText(), pkgName))
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
				richAST.Functions[fullFuncName] = funcMeta
				if ctx.Signature().Type_() != nil {
					funcMeta.ReturnType = a.resolveType(ctx.Signature().Type_().GetText(), pkgName)
				}
				if ctx.Signature().Parameters() != nil {
					pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
					if pList := pCtx.ParameterList(); pList != nil {
						for _, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveType(paramCtx.Type_().GetText(), pkgName))
							} else {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
							}
						}
					}
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
				richAST.Functions[funcName] = funcMeta
			}
		}
	}

	// 3. Discover companion objects - types with Unapply methods that can be used for pattern matching
	a.discoverCompanionObjects(richAST)

	return richAST, nil
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

func (a *galaAnalyzer) resolveType(typeName string, pkgName string) transpiler.Type {
	return a.resolveTypeWithParams(typeName, pkgName, nil)
}

// resolveTypeWithParams resolves a type name, taking into account type parameters
// that should not be prefixed with the package name.
func (a *galaAnalyzer) resolveTypeWithParams(typeName string, pkgName string, typeParams []string) transpiler.Type {
	if typeName == "" {
		return transpiler.NilType{}
	}
	// If it's already package-qualified, just parse it
	if strings.Contains(typeName, ".") {
		return transpiler.ParseType(typeName)
	}

	// Check if it's a type parameter - these should not be prefixed
	for _, tp := range typeParams {
		if typeName == tp {
			return transpiler.ParseType(typeName)
		}
	}

	// Check if it's a builtin
	switch typeName {
	case "int", "int32", "int64", "float32", "float64", "string", "bool", "any", "error":
		return transpiler.ParseType(typeName)
	}

	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		return transpiler.ParseType(pkgName + "." + typeName)
	}
	return transpiler.ParseType(typeName)
}

func (a *galaAnalyzer) analyzePackage(relPath string) (*transpiler.RichAST, error) {
	var dirPath string
	found := false

	// First, try module-based resolution if we have a module root
	if a.moduleRoot != "" && a.moduleName != "" {
		// Check if relPath starts with module name (e.g., "martianoff/gala/std")
		if strings.HasPrefix(relPath, a.moduleName+"/") {
			// Strip module prefix and resolve from module root
			localPath := strings.TrimPrefix(relPath, a.moduleName+"/")
			dirPath = filepath.Join(a.moduleRoot, localPath)
			if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
				found = true
			}
		} else if !strings.Contains(relPath, "/") {
			// Simple package name like "std" - resolve from module root
			dirPath = filepath.Join(a.moduleRoot, relPath)
			if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
				found = true
			}
		}
	}

	// Fall back to search paths if module resolution failed
	if !found {
		for _, p := range a.searchPaths {
			dirPath = filepath.Join(p, relPath)
			if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
				found = true
				break
			}
		}
	}

	if !found {
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
					return nil, fmt.Errorf("multiple package names in directory %s: %s and %s", dirPath, pkgAST.PackageName, res.PackageName)
				}
				pkgAST.Merge(res)
			}
		}
	}
	return pkgAST, nil
}

func getBaseTypeName(ctx grammar.ITypeContext) string {
	if ctx == nil {
		return ""
	}
	if ctx.Identifier() != nil {
		return ctx.Identifier().GetText()
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

var _ transpiler.Analyzer = (*galaAnalyzer)(nil)
