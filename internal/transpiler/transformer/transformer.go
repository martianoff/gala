package transformer

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"io"
	"os"
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
	importManager         *ImportManager                                 // unified import tracking (includes transitive imports and dot-import usage)
	tempVarCount          int
	inferer               *infer.Inferer
	currentFuncReturnType    transpiler.Type            // return type of the function currently being transformed
	currentMatchSubjectType transpiler.Type            // type of the match expression's subject (for branch type inference)
	typeAliases           map[string]transpiler.Type // type alias name -> underlying type (e.g., "Handler" -> func(string) Future[string])
	goTypeInfo            *transpiler.GoTypeInfo     // type info from Go packages (stdlib, local Go files, third-party)
	filePath              string                      // source file path (for error reporting)
	sourceLines           []string                    // source lines (for error snippets)
	richAST               *transpiler.RichAST         // reference to the primary RichAST for live metadata access
	traceTypeResolution   bool                        // when true, type resolution events are recorded
	typeTraces            []TypeTraceEntry             // recorded type resolution events (only when tracing is enabled)
	exprTypeCache         map[ast.Expr]transpiler.Type // cache for getExprTypeNameManual results
	needsEmbedImport      bool                        // true when embed val declarations require import "embed"
	warnTypeInference     bool                        // when true, log warnings about type inference fallbacks
	inferenceWarnings     []string                    // collected type inference warnings
	structMetas           map[string]*structMetaConfig  // generated StructMeta structs (keyed by generated name)
	instanceInterfaceNames map[string]string            // type name -> actual generated interface name (for collision avoidance)
	expectedIfExprType     ast.Expr                     // FIX-052: expected return type for if-expression IIFE (set by expression-bodied function handler)
	lspVarTypes            map[string]transpiler.Type   // LSP: collects all resolved var types during transformation
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
		structMetas:       make(map[string]*structMetaConfig),
	}
}

func (t *galaASTTransformer) TransformForLSP(richAST *transpiler.RichAST) (*transpiler.TransformResult, error) {
	fset, file, transformErr := t.Transform(richAST)
	// Always return collected var types, even on error (partial results)
	varTypes := make(map[string]transpiler.Type, len(t.lspVarTypes))
	for name, typ := range t.lspVarTypes {
		varTypes[name] = typ
	}
	return &transpiler.TransformResult{
		Fset:     fset,
		File:     file,
		VarTypes: varTypes,
	}, transformErr
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
	t.lspVarTypes = make(map[string]transpiler.Type)
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
	t.typeAliases = make(map[string]transpiler.Type)
	// Load type aliases from sibling files (extracted by analyzer)
	for name, underlyingType := range richAST.TypeAliases {
		t.typeAliases[name] = underlyingType
	}
	t.goTypeInfo = richAST.GoTypeInfo
	t.tempVarCount = 0
	t.structMetas = make(map[string]*structMetaConfig)
	t.richAST = richAST
	t.traceTypeResolution = os.Getenv("GALA_TRACE_TYPES") == "1"
	t.warnTypeInference = os.Getenv("GALA_WARN_TYPES") == "1"
	t.typeTraces = nil
	t.inferenceWarnings = nil
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

	// Fail hard if GALA package analysis had unresolved imports.
	// Without resolved package metadata, type inference degrades to `any` and
	// the generated Go code will not compile.
	if len(richAST.AnalysisWarnings) > 0 {
		var msgs []string
		for _, w := range richAST.AnalysisWarnings {
			msgs = append(msgs, "  - "+w)
		}
		return nil, nil, galaerr.NewSemanticError(
			fmt.Sprintf("cannot transpile: %d imported package(s) could not be resolved:\n%s\n"+
				"Hint: ensure all GALA dependencies are available via --search paths or gala.mod",
				len(richAST.AnalysisWarnings), strings.Join(msgs, "\n")))
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
	if err := t.importManager.ValidateDotImports(richAST); err != nil {
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

	// Finalize codec/StructMeta declarations (generate Go AST for all collected intrinsics)
	t.finalizeCodecs(file)

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
	t.importManager.AddTransitiveImportsToFile(file)

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

	// Dump type inference warnings if enabled
	if t.warnTypeInference && len(t.inferenceWarnings) > 0 {
		fmt.Fprintf(os.Stderr, "=== Type Inference Warnings (%s) ===\n", t.filePath)
		for _, w := range t.inferenceWarnings {
			fmt.Fprintf(os.Stderr, "  WARN: %s\n", w)
		}
		fmt.Fprintf(os.Stderr, "=== End Warnings (%d) ===\n", len(t.inferenceWarnings))
	}

	// Remove unused imports from the generated AST.
	t.importManager.PruneUnused(file, richAST)

	return fset, file, nil
}

// markDotImportUsed records that a symbol from a dot-imported package was referenced.
// Delegates to ImportManager.MarkDotImportUsed for unified tracking.
func (t *galaASTTransformer) markDotImportUsed(pkgName string) {
	t.importManager.MarkDotImportUsed(pkgName)
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

	// 2. If typeName has a package prefix (e.g., "pkg.Type"), extract the simple name
	// and try resolving it — but ONLY if the package is a GALA package (has types
	// registered from GALA analysis). Go packages (time, fmt, httpcore, go_struct_bridge)
	// should NOT have their types resolved to same-named GALA types.
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		pkgPrefix := typeName[:idx]
		simpleName := typeName[idx+1:]

		// Resolve import alias to actual package name (e.g., "libalias" -> "lib",
		// "im" -> "collection_immutable") since types are registered under actual
		// package names, not user-chosen aliases.
		actualPkg := pkgPrefix
		if resolved, ok := t.importManager.ResolveAlias(pkgPrefix); ok {
			actualPkg = resolved
		}

		// Try exact match with the resolved package name first
		// (e.g., "collection_immutable.HashMap" when alias was "im")
		if actualPkg != pkgPrefix {
			resolvedName := actualPkg + "." + simpleName
			if exists(resolvedName) {
				return resolvedName, true
			}
		}

		// A package is a GALA package if any of its types exist in typeMetas
		// (registered during GALA source analysis). Go packages won't have entries.
		isGalaPackage := false
		for key := range t.typeMetas {
			if strings.HasPrefix(key, pkgPrefix+".") || strings.HasPrefix(key, actualPkg+".") {
				isGalaPackage = true
				break
			}
		}

		// Only resolve the simple name to a GALA type if the qualifier is a GALA package
		if isGalaPackage {
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
