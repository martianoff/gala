package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"
	"martianoff/gala/internal/transpiler/registry"
)

type galaASTTransformer struct {
	currentScope          *scope
	packageName           string
	immutFields           map[string]bool
	structImmutFields     map[string][]bool
	needsStdImport        bool
	needsFmtImport        bool
	activeTypeParams      map[string]bool
	structFields          map[string][]string
	structFieldTypes      map[string]map[string]transpiler.Type // structName -> fieldName -> typeName
	genericMethods        map[string]map[string]bool            // receiverType -> methodName -> isGeneric
	functions             map[string]*transpiler.FunctionMetadata
	typeMetas             map[string]*transpiler.TypeMetadata
	companionObjects      map[string]*transpiler.CompanionObjectMetadata // companion name -> metadata
	importManager         *ImportManager                                 // unified import tracking
	tempVarCount          int
	inferer               *infer.Inferer
	currentFuncReturnType transpiler.Type // return type of the function currently being transformed
	filePath              string           // source file path (for error reporting)
	sourceLines           []string         // source lines (for error snippets)
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:       make(map[string]bool),
		structImmutFields: make(map[string][]bool),
		activeTypeParams:  make(map[string]bool),
		structFields:      make(map[string][]string),
		structFieldTypes:  make(map[string]map[string]transpiler.Type),
		genericMethods:    make(map[string]map[string]bool),
		functions:         make(map[string]*transpiler.FunctionMetadata),
		typeMetas:         make(map[string]*transpiler.TypeMetadata),
		companionObjects:  make(map[string]*transpiler.CompanionObjectMetadata),
		importManager:     NewImportManager(),
		inferer:           infer.NewInferer(),
	}
}

func (t *galaASTTransformer) Transform(richAST *transpiler.RichAST) (fset *token.FileSet, file *ast.File, err error) {
	defer func() {
		if r := recover(); r != nil {
			if semErr, ok := r.(*galaerr.SemanticError); ok {
				err = semErr
			} else {
				panic(r)
			}
		}
	}()
	tree := richAST.Tree
	t.currentScope = nil
	t.needsStdImport = false
	t.needsFmtImport = false
	t.immutFields = make(map[string]bool)
	t.structImmutFields = make(map[string][]bool)
	t.activeTypeParams = make(map[string]bool)
	t.structFields = make(map[string][]string)
	t.structFieldTypes = make(map[string]map[string]transpiler.Type)
	t.genericMethods = make(map[string]map[string]bool)
	t.functions = richAST.Functions
	t.typeMetas = richAST.Types
	t.companionObjects = richAST.CompanionObjects
	if t.companionObjects == nil {
		t.companionObjects = make(map[string]*transpiler.CompanionObjectMetadata)
	}
	t.importManager = NewImportManager()
	t.tempVarCount = 0
	t.filePath = richAST.FilePath
	if richAST.SourceContent != "" {
		t.sourceLines = strings.Split(richAST.SourceContent, "\n")
	} else {
		t.sourceLines = nil
	}

	// Populate imports from richAST.Packages (includes implicit std import from analyzer)
	t.importManager.AddFromPackages(richAST.Packages)

	// Populate metadata from RichAST
	for typeName, meta := range richAST.Types {
		t.structFieldTypes[typeName] = meta.Fields
		t.structFields[typeName] = meta.FieldNames
		t.structImmutFields[typeName] = meta.ImmutFlags
		if _, ok := t.genericMethods[typeName]; !ok {
			t.genericMethods[typeName] = make(map[string]bool)
		}
		for methodName, methodMeta := range meta.Methods {
			if len(methodMeta.TypeParams) > 0 || methodMeta.IsGeneric {
				t.genericMethods[typeName][methodName] = true
			}
		}
	}

	t.pushScope() // Global scope
	defer t.popScope()

	fset = token.NewFileSet()
	sourceFile, ok := any(tree).(*grammar.SourceFileContext)
	if !ok {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("expected *grammar.SourceFileContext, got %T", tree))
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
	t.packageName = pkgName
	file = &ast.File{
		Name: ast.NewIdent(pkgName),
	}

	// Imports
	for _, importCtx := range sourceFile.AllImportDeclaration() {
		decl, err := t.transformImportDeclaration(importCtx.(*grammar.ImportDeclarationContext))
		if err != nil {
			return nil, nil, err
		}
		file.Decls = append(file.Decls, decl)
	}

	// Update actual package names from richAST.Packages for better type resolution
	for path, actualPkgName := range richAST.Packages {
		t.importManager.UpdateActualPackageName(path, actualPkgName)
	}

	// Error on symbol clashes between dot-imported packages
	if err := t.checkDotImportClashes(richAST); err != nil {
		return nil, nil, err
	}

	for _, topDeclCtx := range sourceFile.AllTopLevelDeclaration() {
		decls, err := t.transformTopLevelDeclaration(topDeclCtx)
		if err != nil {
			return nil, nil, err
		}
		if decls != nil {
			file.Decls = append(file.Decls, decls...)
		}
	}

	if t.needsStdImport && t.packageName != registry.StdPackageName {
		// Check if std is already imported (e.g., as a dot import)
		stdAlreadyImported := t.importManager.IsDotImported(registry.StdPackageName)
		if !stdAlreadyImported {
			// Add import at the beginning
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: fmt.Sprintf("\"%s\"", registry.StdImportPath),
						},
					},
				},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	if t.needsFmtImport {
		_, hasFmt := t.importManager.GetByPath("fmt")

		if !hasFmt {
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: "\"fmt\"",
						},
					},
				},
			}
			// If std was added, it's at index 0. We want fmt to be there too.
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	return fset, file, nil
}

// checkDotImportClashes detects when multiple dot-imported packages export symbols with the
// same name, which would cause Go compilation errors ("redeclared in this block").
// Returns a SemanticError listing all clashing symbols.
func (t *galaASTTransformer) checkDotImportClashes(richAST *transpiler.RichAST) error {
	dotPkgs := t.importManager.GetDotImports()
	if len(dotPkgs) < 2 {
		return nil // need at least 2 dot imports for a clash
	}

	dotPkgSet := make(map[string]bool, len(dotPkgs))
	for _, pkg := range dotPkgs {
		dotPkgSet[pkg] = true
	}

	// Collect symbol -> set of source packages
	symbolSources := make(map[string]map[string]bool) // symbol name -> {pkg1, pkg2, ...}

	// Check GALA-analyzed metadata (Types, Functions, CompanionObjects)
	for _, meta := range richAST.Types {
		if meta.Package != "" && dotPkgSet[meta.Package] {
			if symbolSources[meta.Name] == nil {
				symbolSources[meta.Name] = make(map[string]bool)
			}
			symbolSources[meta.Name][meta.Package] = true
		}
	}

	for _, meta := range richAST.Functions {
		if meta.Package != "" && dotPkgSet[meta.Package] {
			if symbolSources[meta.Name] == nil {
				symbolSources[meta.Name] = make(map[string]bool)
			}
			symbolSources[meta.Name][meta.Package] = true
		}
	}

	for _, meta := range richAST.CompanionObjects {
		if meta.Package != "" && dotPkgSet[meta.Package] {
			if symbolSources[meta.Name] == nil {
				symbolSources[meta.Name] = make(map[string]bool)
			}
			symbolSources[meta.Name][meta.Package] = true
		}
	}

	// Check Go-only package exports (from GoExports field)
	for pkg, symbols := range richAST.GoExports {
		if !dotPkgSet[pkg] {
			continue
		}
		for _, sym := range symbols {
			if symbolSources[sym] == nil {
				symbolSources[sym] = make(map[string]bool)
			}
			symbolSources[sym][pkg] = true
		}
	}

	// Collect clashes
	var clashes []string
	// Sort symbol names for deterministic output
	symbolNames := make([]string, 0, len(symbolSources))
	for symbol := range symbolSources {
		symbolNames = append(symbolNames, symbol)
	}
	sort.Strings(symbolNames)

	for _, symbol := range symbolNames {
		sources := symbolSources[symbol]
		if len(sources) > 1 {
			pkgs := make([]string, 0, len(sources))
			for pkg := range sources {
				pkgs = append(pkgs, pkg)
			}
			sort.Strings(pkgs)
			clashes = append(clashes, fmt.Sprintf("  - symbol %q is exported by multiple dot-imported packages: %s", symbol, strings.Join(pkgs, ", ")))
		}
	}

	if len(clashes) > 0 {
		msg := "dot-import symbol collision(s) detected:\n" + strings.Join(clashes, "\n") + "\nUse an aliased import for one of the packages to resolve the conflict."
		return galaerr.NewSemanticError(msg)
	}
	return nil
}

// semanticErrorAt creates a SemanticError with position info from an ANTLR context.
func (t *galaASTTransformer) semanticErrorAt(ctx antlr.ParserRuleContext, msg string) *galaerr.SemanticError {
	if ctx != nil && ctx.GetStart() != nil {
		line := ctx.GetStart().GetLine()
		col := ctx.GetStart().GetColumn()
		return galaerr.NewSemanticErrorInFile(t.filePath, line, col, msg)
	}
	return galaerr.NewSemanticError(msg)
}

var _ transpiler.ASTTransformer = (*galaASTTransformer)(nil)

// resolveTypeName is a unified type resolution function that searches for a type name
// using a consistent resolution order. It takes a check function to determine if a
// candidate name exists in the target data structure.
//
// Resolution Order (documented and consistent):
//  1. Exact match
//  2. If name has package prefix: try replacing prefix with std/current/imported packages
//     (but NOT for external Go packages like "time", "fmt", etc.)
//  3. Try current package prefix
//  4. Try std package prefix
//  5. Try all explicitly imported packages (non-dot)
//  6. Try dot-imported packages
//
// Returns the resolved name and whether resolution succeeded.
func (t *galaASTTransformer) resolveTypeName(typeName string, exists func(string) bool) (string, bool) {
	// 1. Try exact match first
	if exists(typeName) {
		return typeName, true
	}

	// 2. If typeName has a package prefix, extract the simple name and try other packages
	// BUT only if the package prefix is NOT from an external (non-GALA) import
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		pkgPrefix := typeName[:idx]
		simpleName := typeName[idx+1:]

		// Check if this is an external package (imported Go package like "time", "fmt", etc.)
		// If so, don't try to resolve the simple name to GALA types - external types
		// like time.Duration should not be confused with GALA's Duration type
		isExternalPackage := false
		for _, entry := range t.importManager.All() {
			// Get the alias used in code (e.g., "time" for import "time")
			alias := entry.Alias
			if alias == "" {
				// Extract last component from import path (e.g., "time" from "time")
				if lastSlash := strings.LastIndex(entry.Path, "/"); lastSlash != -1 {
					alias = entry.Path[lastSlash+1:]
				} else {
					alias = entry.Path
				}
			}
			if alias == pkgPrefix && !entry.IsDot {
				// Check if it's a GALA package by looking at the import path
				// GALA packages typically have paths containing "/gala/"
				if !strings.Contains(entry.Path, "/gala/") {
					isExternalPackage = true
					break
				}
			}
		}

		// Only try to resolve the simple name if it's not from an external package
		if !isExternalPackage {
			if resolved, found := t.tryResolveSimpleName(simpleName, exists); found {
				return resolved, true
			}
		}
	}

	// 3. Try resolving the original typeName through all package prefixes
	if resolved, found := t.tryResolveSimpleName(typeName, exists); found {
		return resolved, true
	}

	return "", false
}

// tryResolveSimpleName attempts to resolve a simple (unqualified) type name
// by trying various package prefixes in order of precedence.
func (t *galaASTTransformer) tryResolveSimpleName(name string, exists func(string) bool) (string, bool) {
	// Try simple name without any package prefix first
	if exists(name) {
		return name, true
	}

	// Try std package for standard library types like Tuple, Option, etc.
	if stdName := registry.StdPackageName + "." + name; exists(stdName) {
		return stdName, true
	}

	// Try current package
	if t.packageName != "" {
		if fullName := t.packageName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	// Try imported packages (dot imports first — they bring names into the current scope,
	// so they take precedence over non-dot imports for unqualified name resolution)
	for _, entry := range t.importManager.All() {
		if !entry.IsDot {
			continue
		}
		if fullName := entry.PkgName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	for _, entry := range t.importManager.All() {
		if entry.IsDot {
			continue
		}
		if fullName := entry.PkgName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	return "", false
}

// resolveStructTypeName resolves a type name to the key used in structFields/structImmutFields maps.
// Returns the original typeName if not found (for backward compatibility).
func (t *galaASTTransformer) resolveStructTypeName(typeName string) string {
	resolved, found := t.resolveTypeName(typeName, func(name string) bool {
		_, ok := t.structFields[name]
		return ok
	})
	if found {
		return resolved
	}
	return typeName
}

// resolveTypeMetaName resolves a type name to the key used in typeMetas map.
// Returns empty string if not found.
func (t *galaASTTransformer) resolveTypeMetaName(typeName string) string {
	resolved, _ := t.resolveTypeName(typeName, func(name string) bool {
		_, ok := t.typeMetas[name]
		return ok
	})
	return resolved
}

// getTypeMeta resolves a type name and returns the corresponding TypeMetadata.
// This is the preferred method for accessing type metadata - it handles all
// resolution scenarios including package prefixes, std library fallback, and imports.
//
// Resolution precedence:
//  1. Exact match
//  2. std package prefix (for standard library types)
//  3. Current package prefix
//  4. Explicitly imported packages
//  5. Dot-imported packages
//
// Returns nil if the type is not found.
func (t *galaASTTransformer) getTypeMeta(typeName string) *transpiler.TypeMetadata {
	resolved := t.resolveTypeMetaName(typeName)
	if resolved == "" {
		return nil
	}
	return t.typeMetas[resolved]
}

// getTypeMetaResolved returns the type metadata and the resolved (canonical) type name.
// Use this when you need both the metadata and the resolved name to avoid double resolution.
func (t *galaASTTransformer) getTypeMetaResolved(typeName string) (*transpiler.TypeMetadata, string) {
	resolved := t.resolveTypeMetaName(typeName)
	if resolved == "" {
		return nil, ""
	}
	return t.typeMetas[resolved], resolved
}
