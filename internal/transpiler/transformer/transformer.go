package transformer

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"
	"martianoff/gala/internal/transpiler/registry"
	"martianoff/gala/internal/transpiler/resolver"
)

// TypeTraceEntry records a single type resolution event for diagnostics.
type TypeTraceEntry struct {
	ExprStr string          // formatted expression
	Result  transpiler.Type // resolved type
	Method  string          // which resolution method was used
	File    string          // current file being transpiled
	Line    int             // line in source
}

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
	additionalImports     map[string]string                              // path -> alias for transitive imports needed by type inference
	tempVarCount          int
	inferer               *infer.Inferer
	currentFuncReturnType transpiler.Type            // return type of the function currently being transformed
	typeAliases           map[string]transpiler.Type // type alias name -> underlying type (e.g., "Handler" -> func(string) Future[string])
	filePath              string                      // source file path (for error reporting)
	sourceLines           []string                    // source lines (for error snippets)
	richAST               *transpiler.RichAST         // reference to the primary RichAST for live metadata access
	traceTypeResolution   bool                        // when true, type resolution events are recorded
	typeTraces            []TypeTraceEntry             // recorded type resolution events (only when tracing is enabled)
	exprTypeCache         map[ast.Expr]transpiler.Type // cache for getExprTypeNameManual results
	needsEmbedImport      bool                        // true when embed val declarations require import "embed"
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
		typeAliases:       make(map[string]transpiler.Type),
		exprTypeCache:     make(map[ast.Expr]transpiler.Type),
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
	t.additionalImports = make(map[string]string)
	t.typeAliases = make(map[string]transpiler.Type)
	// Load type aliases from sibling files (extracted by analyzer)
	for name, underlyingType := range richAST.TypeAliases {
		t.typeAliases[name] = underlyingType
	}
	t.tempVarCount = 0
	t.richAST = richAST
	t.traceTypeResolution = os.Getenv("GALA_TRACE_TYPES") == "1"
	t.typeTraces = nil
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

	// Register EmbeddedFS method metadata (Go-defined type, not available from GALA analysis).
	// This enables type inference for ReadString/ReadBytes calls on embedded filesystems.
	t.registerEmbeddedFSMetadata()

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

	// Add transitive imports needed by type inference (e.g., when lambda parameter
	// types are inferred from a dependency's method signature and reference packages
	// not explicitly imported in the current file).
	for path, alias := range t.additionalImports {
		// Skip if already imported explicitly
		alreadyImported := false
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.IMPORT {
				continue
			}
			for _, spec := range genDecl.Specs {
				importSpec, ok := spec.(*ast.ImportSpec)
				if !ok {
					continue
				}
				importPath := strings.Trim(importSpec.Path.Value, "\"")
				if importPath == path {
					alreadyImported = true
					break
				}
				// Also check by alias — if the package is imported under a different path
				if importSpec.Name != nil && importSpec.Name.Name == alias {
					alreadyImported = true
					break
				}
			}
			if alreadyImported {
				break
			}
		}
		if !alreadyImported {
			spec := &ast.ImportSpec{
				Path: &ast.BasicLit{
					Kind:  token.STRING,
					Value: fmt.Sprintf("\"%s\"", path),
				},
			}
			importDecl := &ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{spec},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	// Add import "embed" when embed val declarations with EmbeddedFS type are present.
	// For string embeds, Go requires import _ "embed" (blank import).
	if t.needsEmbedImport {
		hasEmbedImport := false
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.IMPORT {
				continue
			}
			for _, spec := range genDecl.Specs {
				importSpec, ok := spec.(*ast.ImportSpec)
				if !ok {
					continue
				}
				if strings.Trim(importSpec.Path.Value, "\"") == "embed" {
					hasEmbedImport = true
					break
				}
			}
			if hasEmbedImport {
				break
			}
		}
		if !hasEmbedImport {
			// Determine if we need a blank import or a named import.
			// EmbeddedFS uses embed.FS directly, so we need the named import.
			// string/[]byte embeds only need _ "embed".
			hasEmbeddedFS := false
			for _, ed := range richAST.EmbedDirectives {
				if ed.TypeName == transpiler.TypeEmbeddedFS || ed.TypeName == "std.EmbeddedFS" {
					hasEmbeddedFS = true
					break
				}
			}
			spec := &ast.ImportSpec{
				Path: &ast.BasicLit{
					Kind:  token.STRING,
					Value: "\"embed\"",
				},
			}
			if !hasEmbeddedFS {
				// Blank import for string/[]byte embeds
				spec.Name = ast.NewIdent("_")
			}
			importDecl := &ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{spec},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	// Dump type resolution traces to stderr when tracing is enabled.
	if t.traceTypeResolution && len(t.typeTraces) > 0 {
		fmt.Fprintf(os.Stderr, "=== Type Resolution Trace (%s) ===\n", t.filePath)
		t.DumpTypeTrace(os.Stderr)
		fmt.Fprintf(os.Stderr, "=== End Trace (%d entries) ===\n", len(t.typeTraces))
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
// Delegates to the shared resolver.TypeResolver for consistent resolution logic.
func (t *galaASTTransformer) tryResolveSimpleName(name string, exists func(string) bool) (string, bool) {
	return t.buildTypeResolver().Resolve(name, exists)
}

// buildTypeResolver creates a resolver.TypeResolver from the transformer's current state.
func (t *galaASTTransformer) buildTypeResolver() *resolver.TypeResolver {
	var imports []resolver.PackageInfo
	for _, entry := range t.importManager.All() {
		imports = append(imports, resolver.PackageInfo{
			PkgName: entry.PkgName,
			IsDot:   entry.IsDot,
		})
	}
	return &resolver.TypeResolver{
		PackageName: t.packageName,
		Imports:     imports,
	}
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
		// Fallback: check the primary RichAST directly in case metadata was added
		// after the initial copy (e.g., by sibling scanning or late analysis).
		if t.richAST != nil {
			resolved2, found := t.resolveTypeName(typeName, func(name string) bool {
				_, ok := t.richAST.Types[name]
				return ok
			})
			if found {
				return t.richAST.Types[resolved2]
			}
		}
		return nil
	}
	return t.typeMetas[resolved]
}

// getTypeMetaResolved returns the type metadata and the resolved (canonical) type name.
// Use this when you need both the metadata and the resolved name to avoid double resolution.
func (t *galaASTTransformer) getTypeMetaResolved(typeName string) (*transpiler.TypeMetadata, string) {
	resolved := t.resolveTypeMetaName(typeName)
	if resolved == "" {
		// Fallback: check the primary RichAST directly for late-added metadata.
		if t.richAST != nil {
			resolved2, found := t.resolveTypeName(typeName, func(name string) bool {
				_, ok := t.richAST.Types[name]
				return ok
			})
			if found {
				return t.richAST.Types[resolved2], resolved2
			}
		}
		return nil, ""
	}
	return t.typeMetas[resolved], resolved
}

// traceType records a type resolution event when tracing is enabled.
// This is a no-op when traceTypeResolution is false, so it is safe to
// call on every resolution path without measurable overhead.
func (t *galaASTTransformer) traceType(expr ast.Expr, result transpiler.Type, method string) {
	if !t.traceTypeResolution {
		return
	}
	line := 0
	// We don't have a Go token.FileSet for position mapping (the GALA source
	// positions come from ANTLR, not from Go AST nodes), so line stays 0 for
	// Go AST expressions. The file path is still useful for multi-file builds.
	t.typeTraces = append(t.typeTraces, TypeTraceEntry{
		ExprStr: formatExprForTrace(expr),
		Result:  result,
		Method:  method,
		File:    t.filePath,
		Line:    line,
	})
}

// registerEmbeddedFSMetadata adds type metadata for std.EmbeddedFS.
// EmbeddedFS is defined in Go (std/embedded_fs.go), so it's not discovered
// by the GALA analyzer. We register its method signatures manually so that
// type inference works for ReadString/ReadBytes calls.
func (t *galaASTTransformer) registerEmbeddedFSMetadata() {
	fullName := "std." + transpiler.TypeEmbeddedFS
	if _, exists := t.typeMetas[fullName]; exists {
		return // Already registered (e.g., from sibling analysis)
	}
	meta := &transpiler.TypeMetadata{
		Name:    transpiler.TypeEmbeddedFS,
		Package: "std",
		Methods: map[string]*transpiler.MethodMetadata{
			"ReadString": {
				Name:       "ReadString",
				Package:    "std",
				ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "string"}},
				ReturnType: transpiler.GenericType{
					Base:   transpiler.NamedType{Package: "std", Name: "Try"},
					Params: []transpiler.Type{transpiler.BasicType{Name: "string"}},
				},
			},
		},
		Fields:     make(map[string]transpiler.Type),
		FieldNames: nil,
	}
	t.typeMetas[fullName] = meta
	// Also register without package prefix for dot-imported std
	if _, exists := t.typeMetas[transpiler.TypeEmbeddedFS]; !exists {
		t.typeMetas[transpiler.TypeEmbeddedFS] = meta
	}
}

// DumpTypeTrace writes all recorded type resolution events to w.
// Each line has the format: [file:line] exprStr -> result (via method)
func (t *galaASTTransformer) DumpTypeTrace(w io.Writer) {
	for _, entry := range t.typeTraces {
		resultStr := "<nil>"
		if entry.Result != nil {
			resultStr = entry.Result.String()
			if resultStr == "" {
				resultStr = "<NilType>"
			}
		}
		fmt.Fprintf(w, "[%s:%d] %s -> %s (via %s)\n", entry.File, entry.Line, entry.ExprStr, resultStr, entry.Method)
	}
}

// formatExprForTrace produces a short human-readable representation of a Go AST expression.
func formatExprForTrace(expr ast.Expr) string {
	if expr == nil {
		return "<nil>"
	}
	// Use go/printer for a compact representation, falling back to type name.
	var buf strings.Builder
	fset := token.NewFileSet()
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return fmt.Sprintf("<%T>", expr)
	}
	s := buf.String()
	// Truncate very long expressions for readability.
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
