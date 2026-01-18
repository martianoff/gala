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

// GetBaseMetadata loads standard library metadata from .gala files.
func GetBaseMetadata(p transpiler.GalaParser, searchPaths []string) *transpiler.RichAST {
	base := &transpiler.RichAST{
		Types:            make(map[string]*transpiler.TypeMetadata),
		Functions:        make(map[string]*transpiler.FunctionMetadata),
		Packages:         make(map[string]string),
		CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
	}
	base.Packages[transpiler.StdImportPath] = transpiler.StdPackage
	temp := NewGalaAnalyzer(p, searchPaths)
	stdFiles := []string{"std/option.gala", "std/immutable.gala", "std/tuple.gala", "std/either.gala"}

	for _, sf := range stdFiles {
		var content []byte
		var err error
		for _, path := range searchPaths {
			content, err = ioutil.ReadFile(filepath.Join(path, sf))
			if err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		tree, err := p.Parse(string(content))
		if err != nil {
			continue
		}
		rich, err := temp.Analyze(tree, sf)
		if err != nil {
			continue
		}
		base.Merge(rich)
	}
	return base
}

type galaAnalyzer struct {
	baseMetadata *transpiler.RichAST
	parser       transpiler.GalaParser
	searchPaths  []string
	analyzedPkgs map[string]bool
	checkedDirs  map[string]bool
}

// NewGalaAnalyzer creates a new transpiler.Analyzer implementation.
func NewGalaAnalyzer(p transpiler.GalaParser, searchPaths []string) transpiler.Analyzer {
	return &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]bool),
		checkedDirs:  make(map[string]bool),
	}
}

// NewGalaAnalyzerWithBase creates a new transpiler.Analyzer with base metadata.
func NewGalaAnalyzerWithBase(base *transpiler.RichAST, p transpiler.GalaParser, searchPaths []string) transpiler.Analyzer {
	return &galaAnalyzer{
		baseMetadata: base,
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]bool),
		checkedDirs:  make(map[string]bool),
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

	// 0. Populate base metadata if provided
	if a.baseMetadata != nil {
		richAST.Merge(a.baseMetadata)
	}

	// 0.5 Scan imports
	for _, impDecl := range sourceFile.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			path := strings.Trim(s.STRING().GetText(), "\"")
			if strings.HasPrefix(path, "martianoff/gala/") {
				relPath := strings.TrimPrefix(path, "martianoff/gala/")
				if !a.analyzedPkgs[path] {
					a.analyzedPkgs[path] = true
					importedAST, err := a.analyzePackage(relPath)
					if err == nil {
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
	for _, p := range a.searchPaths {
		dirPath = filepath.Join(p, relPath)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			found = true
			break
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
