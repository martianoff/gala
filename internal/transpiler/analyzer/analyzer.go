package analyzer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/module"
	"martianoff/gala/internal/transpiler/profiler"
	"martianoff/gala/internal/transpiler/registry"
	"martianoff/gala/internal/transpiler/resolver"
	"martianoff/gala/internal/transpiler/transformer"
)

// GetBaseMetadata loads standard library metadata for use in tests and backward compatibility.
// In normal compilation flow, std is loaded via implicit import in Analyze().
func GetBaseMetadata(p transpiler.GalaParser, searchPaths []string) *transpiler.RichAST {
	a := &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
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
	resolver            *module.Resolver               // Handles module root discovery and package path resolution
	currentRichAST      *transpiler.RichAST            // Set during Analyze() for cross-reference in resolveTypeWithParams
	currentDotImportPkgs map[string]bool                // Package names that are dot-imported in the current file
	analyzeDepth int                                    // recursion depth for profiling
	cache        *analysisCache                         // disk-based package analysis cache

	// P1 (perf): per-analyzer in-memory cache of parsed sibling ASTs.
	// Key is the canonical directory path; value holds the trees and paths
	// captured on the first scan. Reused across Analyze() calls within the
	// same process so a 5-file package does not re-read and re-parse its
	// siblings 5 times — the dominant cost in the analyze phase (86.9% of
	// total transpile time for collection_immutable/list.gala, baseline
	// 2.6s on Windows).
	siblingTreeCache map[string]*siblingCacheEntry
}

// siblingCacheEntry is the value stored in galaAnalyzer.siblingTreeCache.
// dirSize captures the number of .gala files discovered during the
// initial scan; on a cache hit we revalidate by re-checking that count.
// If the directory listing has changed shape since we cached, we drop
// the entry and re-scan from disk.
type siblingCacheEntry struct {
	trees   []*grammar.SourceFileContext
	paths   []string
	dirSize int
}

// resolveCacheRoot returns the project root for the analysis disk cache.
// If projectRoot is provided, uses it directly. Otherwise falls back to
// walking up from cwd to find go.mod or gala.mod.
func resolveCacheRoot(projectRoot string) string {
	if projectRoot != "" {
		return projectRoot
	}
	cwd, _ := os.Getwd()
	return findProjectRoot(cwd)
}

// NewGalaAnalyzer creates a new transpiler.Analyzer implementation.
// projectRoot is the directory containing gala.mod — used for the analysis disk cache.
// Pass "" to auto-detect from the current working directory.
func NewGalaAnalyzer(p transpiler.GalaParser, searchPaths []string, projectRoot ...string) transpiler.Analyzer {
	root := ""
	if len(projectRoot) > 0 {
		root = projectRoot[0]
	}
	return &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
		resolver:     module.NewResolver(searchPaths),
		cache:        newAnalysisCache(resolveCacheRoot(root)),
	}
}

// NewGalaAnalyzerWithBase creates a new transpiler.Analyzer with base metadata.
// projectRoot is the directory containing gala.mod — used for the analysis disk cache.
// Pass "" to auto-detect from the current working directory.
func NewGalaAnalyzerWithBase(base *transpiler.RichAST, p transpiler.GalaParser, searchPaths []string, projectRoot ...string) transpiler.Analyzer {
	root := ""
	if len(projectRoot) > 0 {
		root = projectRoot[0]
	}
	return &galaAnalyzer{
		baseMetadata: base,
		parser:       p,
		searchPaths:  searchPaths,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
		resolver:     module.NewResolver(searchPaths),
		cache:        newAnalysisCache(resolveCacheRoot(root)),
	}
}

// NewGalaAnalyzerWithPackageFiles creates an analyzer that uses explicit package file list
// for sibling discovery instead of directory scanning. This enables full cross-file type
// resolution for main/test packages where directory scanning is too broad.
// projectRoot is the directory containing gala.mod — used for the analysis disk cache.
// Pass "" to auto-detect from the current working directory.
func NewGalaAnalyzerWithPackageFiles(p transpiler.GalaParser, searchPaths []string, packageFiles []string, projectRoot ...string) transpiler.Analyzer {
	root := ""
	if len(projectRoot) > 0 {
		root = projectRoot[0]
	}
	return &galaAnalyzer{
		parser:       p,
		searchPaths:  searchPaths,
		packageFiles: packageFiles,
		analyzedPkgs: make(map[string]*transpiler.RichAST),
		checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
		resolver:     module.NewResolver(searchPaths),
		cache:        newAnalysisCache(resolveCacheRoot(root)),
	}
}

// BatchAnalyzer allows transpiling multiple files in a single process, sharing
// the analyzed package cache across files. This avoids redundant re-analysis of
// imports like std, collection_immutable, etc.
type BatchAnalyzer struct {
	inner *galaAnalyzer
}

// NewBatchAnalyzer creates an analyzer optimized for batch transpilation.
// Call SetPackageFiles before each Analyze to configure siblings for that file.
// projectRoot is the directory containing gala.mod — used for the analysis disk cache.
// Pass "" to auto-detect from the current working directory.
func NewBatchAnalyzer(p transpiler.GalaParser, searchPaths []string, projectRoot ...string) *BatchAnalyzer {
	root := ""
	if len(projectRoot) > 0 {
		root = projectRoot[0]
	}
	return &BatchAnalyzer{
		inner: &galaAnalyzer{
			parser:       p,
			searchPaths:  searchPaths,
			analyzedPkgs: make(map[string]*transpiler.RichAST),
			checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
			resolver:     module.NewResolver(searchPaths),
			cache:        newAnalysisCache(resolveCacheRoot(root)),
		},
	}
}

// SetPackageFiles configures the sibling files for the next Analyze call.
// Also resets checkedDirs so directory-based sibling discovery works fresh per file.
func (b *BatchAnalyzer) SetPackageFiles(files []string) {
	b.inner.packageFiles = files
	b.inner.checkedDirs = make(map[string]bool)
}

// Analyze delegates to the inner analyzer, sharing the package cache.
func (b *BatchAnalyzer) Analyze(tree antlr.Tree, filePath string) (*transpiler.RichAST, error) {
	return b.inner.Analyze(tree, filePath)
}

// Analyze walk the ANTLR tree and collects metadata for RichAST.
func (a *galaAnalyzer) Analyze(tree antlr.Tree, filePath string) (*transpiler.RichAST, error) {
	a.analyzeDepth++
	isTopLevel := a.analyzeDepth == 1
	defer func() { a.analyzeDepth-- }()

	var analyzeStart time.Time
	if profiler.Enabled && isTopLevel {
		analyzeStart = time.Now()
	}
	logPhase := func(label string, start time.Time) {
		if profiler.Enabled && isTopLevel {
			fmt.Fprintf(os.Stderr, "  [analyze] %-35s %s\n", label, time.Since(start).Round(time.Millisecond))
		}
	}
	_ = analyzeStart
	_ = logPhase

	sourceFile, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, fmt.Errorf("expected *grammar.SourceFileContext, got %T", tree)
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
	absFilePath, _ := filepath.Abs(filePath)

	phaseStart := time.Now()
	var siblingTrees []*grammar.SourceFileContext
	var siblingPaths []string // parallel slice: file path for each siblingTree
	if len(a.packageFiles) > 0 {
		// Explicit package files: parse each one, validate package name, add to siblings
		for _, pf := range a.packageFiles {
			if isSameFile(pf, filePath) {
				continue // skip self (resolves symlinks for Bazel on Linux)
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
			pkgClause := otherSF.PackageClause().(*grammar.PackageClauseContext)
			otherPkgName := pkgClause.Identifier().GetText()
			if otherPkgName != pkgName {
				return nil, galaerr.NewCodedSemanticError(
					galaerr.CodeDuplicatePackageName,
					pkgClause.GetStart().GetLine(), pkgClause.GetStart().GetColumn(),
					fmt.Sprintf("package file %s declares package %q but sibling files declare %q", pf, otherPkgName, pkgName),
					"use the same package name across all sibling .gala files, or move the file to a different directory",
				)
			}
			siblingTrees = append(siblingTrees, otherSF)
			siblingPaths = append(siblingPaths, pf)
		}
	} else if filePath != "" && pkgName != "main" && pkgName != "test" {
		// Directory-discovered siblings (only for library packages, not main/test).
		// For main/test packages, directory-discovered siblings are independent programs
		// that happen to share a directory (e.g., examples/). Scanning their imports
		// would pollute the current file's type resolution with unrelated packages.
		dirPath := filepath.Dir(filePath)
		canonDir := canonicalPath(dirPath)

		// P1 (perf): try the in-memory sibling AST cache first. Reuses parsed
		// trees across Analyze() calls within the same process, so a 5-file
		// package does not re-read and re-parse its siblings 5 times.
		//
		// Cache validity: re-stat the directory and compare the .gala-file
		// count against the entry's recorded dirSize. If the listing has
		// changed shape, drop the entry and fall through to a fresh scan.
		// This is cheap (one ReadDir + count) and catches the common case
		// where a file was added or removed since the cache was populated.
		trees, paths, err := a.lookupSiblingCache(canonDir, dirPath, filePath, pkgName)
		if err != nil {
			return nil, err
		}
		if trees != nil {
			siblingTrees = append(siblingTrees, trees...)
			siblingPaths = append(siblingPaths, paths...)
		} else if !a.checkedDirs[canonDir] {
			// Cache miss — scan the directory, parse each .gala file, and
			// cache the full set (unfiltered by current file). The per-call
			// checkedDirs guard prevents repeat scans within a single
			// Analyze() invocation; the per-analyzer siblingTreeCache
			// carries the result across subsequent calls.
			a.checkedDirs[canonDir] = true
			files, err := ioutil.ReadDir(dirPath)
			if err == nil {
				var cacheTrees []*grammar.SourceFileContext
				var cachePaths []string
				galaFileCount := 0
				for _, f := range files {
					if f.IsDir() || filepath.Ext(f.Name()) != ".gala" {
						continue
					}
					galaFileCount++
					otherPath := filepath.Join(dirPath, f.Name())
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
					cacheTrees = append(cacheTrees, otherSF)
					cachePaths = append(cachePaths, otherPath)
				}
				// Store the full (unfiltered) set so future calls on the
				// same directory can reuse it without re-parsing.
				a.siblingTreeCache[canonDir] = &siblingCacheEntry{
					trees:   cacheTrees,
					paths:   cachePaths,
					dirSize: galaFileCount,
				}
				// Apply per-call filters to the freshly-scanned set.
				filtered, filteredPaths, err := a.filterSiblingsForCurrentFile(
					cacheTrees, cachePaths, filePath, pkgName, dirPath)
				if err != nil {
					return nil, err
				}
				siblingTrees = append(siblingTrees, filtered...)
				siblingPaths = append(siblingPaths, filteredPaths...)
			}
		}
	}

	logPhase("sibling-discovery ("+fmt.Sprintf("%d files", len(siblingTrees))+")", phaseStart)

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

	phaseStart = time.Now()
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

	logPhase("load-std", phaseStart)
	phaseStart = time.Now()

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

				// Check if the GALA import path differs from the actual Go module path
				// (e.g., "martianoff/gala-server" vs "github.com/martianoff/gala-server")
				if goPath := a.resolver.ResolveGoImportPath(path); goPath != "" {
					if richAST.ImportPathMap == nil {
						richAST.ImportPathMap = make(map[string]string)
					}
					richAST.ImportPathMap[path] = goPath
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
					if err != nil {
						line := s.GetStart().GetLine()
						warnMsg := fmt.Sprintf("failed to analyze package %s (imported at line %d): %v", relPath, line, err)
						fmt.Fprintf(os.Stderr, "Warning: %s\n", warnMsg)
						richAST.AnalysisWarnings = append(richAST.AnalysisWarnings, warnMsg)
					}
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

	// 0.55 Collect import aliases (e.g., import im "path/to/pkg" → im → actual package name)
	for _, impDecl := range sourceFile.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			if aliasIdent := s.Identifier(); aliasIdent != nil {
				alias := aliasIdent.GetText()
				if alias != "." {
					path := strings.Trim(s.STRING().GetText(), "\"")
					if pkgName, ok := richAST.Packages[path]; ok {
						if richAST.ImportAliases == nil {
							richAST.ImportAliases = make(map[string]string)
						}
						richAST.ImportAliases[alias] = pkgName
					}
				}
			}
		}
	}

	logPhase("scan-gala-imports", phaseStart)
	phaseStart = time.Now()

	// 0.6 Analyze Go packages for type information.
	// For non-GALA imports (Go stdlib and third-party Go packages), use go/importer
	// to extract function signatures, type definitions, and type aliases.
	for _, impDecl := range sourceFile.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			path := strings.Trim(s.STRING().GetText(), "\"")

			isInternalGala := strings.HasPrefix(path, "martianoff/gala/")
			isExternalGala := a.resolver.IsGalaPackage(path)
			if isInternalGala || isExternalGala {
				continue // Already handled above
			}

			// This is a Go package — analyze it for type info
			goInfo := AnalyzeGoPackage(path)
			if len(goInfo.Functions) > 0 || len(goInfo.Types) > 0 || len(goInfo.Variables) > 0 || len(goInfo.TypeAliases) > 0 {
				if richAST.GoTypeInfo == nil {
					richAST.GoTypeInfo = transpiler.NewGoTypeInfo()
				}
				richAST.GoTypeInfo.Merge(goInfo)
			}
		}
	}

	logPhase("analyze-go-packages", phaseStart)
	phaseStart = time.Now()

	// 0.75 Also scan sibling imports to ensure all GALA packages used by siblings
	// are loaded into richAST.Types. Without this, resolveTypeWithParams for sibling
	// struct fields can't find types from packages that only siblings import.
	for _, sibTree := range siblingTrees {
		a.scanImports(sibTree, richAST)
	}

	logPhase("scan-sibling-imports", phaseStart)
	phaseStart = time.Now()

	// Build set of dot-imported package names for type resolution.
	// Collect from main file AND all sibling files so that when resolving types in
	// sibling struct fields, we correctly qualify types from their dot imports too.
	dotImportPkgs := make(map[string]bool)
	allSourceFiles := []*grammar.SourceFileContext{sourceFile}
	for _, sib := range siblingTrees {
		allSourceFiles = append(allSourceFiles, sib)
	}
	for _, sf := range allSourceFiles {
		for _, impDecl := range sf.AllImportDeclaration() {
			ctx := impDecl.(*grammar.ImportDeclarationContext)
			for _, spec := range ctx.AllImportSpec() {
				s := spec.(*grammar.ImportSpecContext)
				// Dot import: importSpec has no identifier but has more than just the STRING
				// (the extra child is the '.' terminal)
				isDotImport := s.Identifier() == nil && s.GetChildCount() > 1
				if isDotImport {
					path := strings.Trim(s.STRING().GetText(), "\"")
					if pkgAlias, ok := richAST.Packages[path]; ok {
						dotImportPkgs[pkgAlias] = true
					}
				}
			}
		}
	}
	// std is always implicitly dot-imported
	dotImportPkgs[registry.StdPackageName] = true

	// Set currentRichAST and dot-import tracking so resolveTypeWithParams can check
	// already-known types (from dot-imported packages) before blindly qualifying with
	// the current package name. This prevents e.g. Array from collection_immutable
	// being misqualified as server.Array.
	a.currentRichAST = richAST
	a.currentDotImportPkgs = dotImportPkgs
	defer func() {
		a.currentRichAST = nil
		a.currentDotImportPkgs = nil
	}()

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
				// Error if type is being redefined from a DIFFERENT file.
				// Skip if DefinedIn is empty (cache) or same file (re-analysis from analyzePackage).
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && isSameFile(existing.DefinedIn, absFilePath)) {
					return nil, galaerr.NewCodedSemanticError(
						galaerr.CodeTypeRedefinition,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						fmt.Sprintf("type %q in package %q redefined (first defined in %s)", typeName, pkgName, existing.DefinedIn),
						"remove the duplicate declaration or rename one of the types",
					)
				}
				meta = existing
				// Clear fields to avoid duplicates if re-analyzing
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
				meta.FieldPositions = nil
				meta.Pos = transpiler.PosFromToken(ctx.Identifier().GetStart())
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Pos:     transpiler.PosFromToken(ctx.Identifier().GetStart()),
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
					if meta.FieldPositions == nil {
						meta.FieldPositions = make(map[string]transpiler.SourcePos)
					}
					meta.FieldPositions[fieldName] = transpiler.PosFromToken(fctx.Identifier().GetStart())
				}
				meta.DefinedIn = filePath
			}

			// Extract interface method signatures as type methods
			if ctx.InterfaceType() != nil {
				ifaceType := ctx.InterfaceType().(*grammar.InterfaceTypeContext)
				for _, ms := range ifaceType.AllMethodSpec() {
					msCtx := ms.(*grammar.MethodSpecContext)
					methodName := msCtx.Identifier().GetText()
					methodMeta := &transpiler.MethodMetadata{
						Name:      methodName,
						Package:   pkgName,
						DefinedIn: filePath,
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
			pos := transpiler.PosFromToken(ctx.Identifier().GetStart())
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && isSameFile(existing.DefinedIn, absFilePath)) {
					return nil, galaerr.NewCodedSemanticError(
						galaerr.CodeTypeRedefinition,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						fmt.Sprintf("type %q in package %q redefined (first defined in %s)", typeName, pkgName, existing.DefinedIn),
						"remove the duplicate declaration or rename one of the types",
					)
				}
				meta = existing
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
				meta.FieldPositions = nil
				meta.Pos = pos
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Pos:     pos,
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
						if meta.FieldPositions == nil {
							meta.FieldPositions = make(map[string]transpiler.SourcePos)
						}
						meta.FieldPositions[fieldName] = transpiler.PosFromToken(pctx.Identifier().GetStart())
					}
				}
				meta.DefinedIn = filePath
			}
		}
	}

	logPhase("collect-types", phaseStart)
	phaseStart = time.Now()

	// 1.5 Collect sealed types
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if sealedCtx := topDecl.SealedTypeDeclaration(); sealedCtx != nil {
			ctx := sealedCtx.(*grammar.SealedTypeDeclarationContext)
			sealedName := ctx.Identifier().GetText()
			fullSealedName := sealedName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullSealedName = pkgName + "." + sealedName
			}
			// Check for redefinition (skip if DefinedIn is empty — type came from cache)
			if existing, ok := richAST.Types[fullSealedName]; ok && existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && isSameFile(existing.DefinedIn, absFilePath)) {
				return nil, galaerr.NewCodedSemanticError(
					galaerr.CodeTypeRedefinition,
					ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
					fmt.Sprintf("type %q in package %q redefined (first defined in %s)", sealedName, pkgName, existing.DefinedIn),
					"remove the duplicate declaration or rename one of the types",
				)
			}
			a.analyzeSealedType(ctx, pkgName, richAST)
			if meta, ok := richAST.Types[fullSealedName]; ok {
				meta.DefinedIn = filePath
				for _, v := range meta.SealedVariants {
					companionKey := v.Name
					if pkgName != "" && pkgName != "main" && pkgName != "test" {
						companionKey = pkgName + "." + v.Name
					}
					if cm, ok := richAST.Types[companionKey]; ok {
						cm.DefinedIn = filePath
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
						Name:         methodName,
						Package:      pkgName,
						Pos:          transpiler.PosFromToken(ctx.Identifier().GetStart()),
						ReceiverName: recvCtx.Identifier().GetText(),
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
							for i, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
								} else {
									methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
								}
								if paramCtx.Identifier() != nil {
									methodMeta.ParamNames = append(methodMeta.ParamNames, paramCtx.Identifier().GetText())
								} else {
									methodMeta.ParamNames = append(methodMeta.ParamNames, "")
								}
								// Extract default expression source text
								if paramCtx.ParamDefault() != nil {
									if methodMeta.DefaultExprs == nil {
										methodMeta.DefaultExprs = make(map[int]string)
									}
									defaultCtx := paramCtx.ParamDefault().(*grammar.ParamDefaultContext)
									methodMeta.DefaultExprs[i] = defaultCtx.Expression().GetText()
								}
							}
						}
					}

					if typeMeta, ok := richAST.Types[fullBaseType]; ok {
						if existing, exists := typeMeta.Methods[methodName]; exists {
							// Error if method already has a user-defined implementation
							if existing.DefinedIn != "" && !(existing.DefinedIn != filePath && isSameFile(existing.DefinedIn, absFilePath)) {
								return nil, galaerr.NewCodedSemanticError(
									galaerr.CodeMethodRedefinition,
									ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
									fmt.Sprintf("method %q on type %q in package %q redefined (first defined in %s)", methodName, baseType, pkgName, existing.DefinedIn),
									"remove the duplicate method or rename it",
								)
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
					Name:      funcName,
					Package:   pkgName,
					Pos:       transpiler.PosFromToken(ctx.Identifier().GetStart()),
					DefinedIn: filePath,
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
						for i, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, funcMeta.TypeParams))
							} else {
								funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
							}
							// Extract parameter name
							if paramCtx.Identifier() != nil {
								funcMeta.ParamNames = append(funcMeta.ParamNames, paramCtx.Identifier().GetText())
							} else {
								funcMeta.ParamNames = append(funcMeta.ParamNames, "")
							}
							// Extract default expression source text
							if paramCtx.ParamDefault() != nil {
								if funcMeta.DefaultExprs == nil {
									funcMeta.DefaultExprs = make(map[int]string)
								}
								defaultCtx := paramCtx.ParamDefault().(*grammar.ParamDefaultContext)
								funcMeta.DefaultExprs[i] = defaultCtx.Expression().GetText()
							}
						}
					}
				}
				// Validate default parameter rules
				if err := validateDefaultParams(funcMeta, ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), filePath); err != nil {
					return nil, err
				}
				richAST.Functions[fullFuncName] = funcMeta
			}
		}
	}

	logPhase("collect-methods-functions", phaseStart)
	phaseStart = time.Now()

	// 2.5 Extract sibling file metadata.
	// Both --package-files mode and directory scanning use full metadata extraction
	// (structs, sealed types, methods) to enable complete cross-file type resolution.
	// Directory scanning skips main/test packages since sibling files in those packages
	// may be unrelated programs sharing a directory (e.g., examples/).
	if len(a.packageFiles) > 0 {
		// Explicit package files: full metadata extraction for ALL packages including main/test
		for i, sibTree := range siblingTrees {
			sibPath := ""
			if i < len(siblingPaths) {
				sibPath = siblingPaths[i]
			}
			if err := a.extractSiblingFullMetadata(sibTree, pkgName, richAST, sibPath); err != nil {
				return nil, err
			}
		}
	} else if pkgName != "main" && pkgName != "test" {
		// Directory-discovered siblings: full metadata extraction (same as --package-files mode).
		// Siblings are already filtered to matching package name during discovery (lines above),
		// so it's safe to extract full struct field info without pollution concerns.
		for i, sibTree := range siblingTrees {
			sibPath := ""
			if i < len(siblingPaths) {
				sibPath = siblingPaths[i]
			}
			if err := a.extractSiblingFullMetadata(sibTree, pkgName, richAST, sibPath); err != nil {
				return nil, err
			}
		}
	}

	logPhase("extract-sibling-metadata", phaseStart)
	phaseStart = time.Now()

	// 2.75 Collect embed declarations
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		if embedCtx := topDecl.EmbedDeclaration(); embedCtx != nil {
			ctx := embedCtx.(*grammar.EmbedDeclarationContext)
			varName := ctx.Identifier().GetText()

			// Extract type name if specified
			typeName := ""
			if ctx.Type_() != nil {
				typeName = ctx.Type_().GetText()
			}

			// Extract embed patterns from embedPatterns rule
			patternsCtx := ctx.EmbedPatterns().(*grammar.EmbedPatternsContext)
			var patterns []string
			for _, s := range patternsCtx.AllSTRING() {
				patterns = append(patterns, strings.Trim(s.GetText(), "\""))
			}

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

			richAST.EmbedDirectives = append(richAST.EmbedDirectives, transpiler.EmbedDirective{
				VarName:  varName,
				GoVar:    goVar,
				Patterns: patterns,
				TypeName: typeName,
			})
		}
	}

	// 3. Discover companion objects - types with Unapply methods that can be used for pattern matching
	a.discoverCompanionObjects(richAST)

	// 4. Optional metadata validation (enabled via GALA_VALIDATE_METADATA=1)
	if os.Getenv("GALA_VALIDATE_METADATA") == "1" {
		validationWarnings := ValidateRichAST(richAST)
		for _, w := range validationWarnings {
			fmt.Fprintln(os.Stderr, w.String())
		}
		if HasErrors(validationWarnings) {
			return nil, fmt.Errorf("metadata validation failed with %d error(s)", countErrors(validationWarnings))
		}
		// Append informational warnings to the RichAST for downstream consumers
		for _, w := range validationWarnings {
			richAST.AnalysisWarnings = append(richAST.AnalysisWarnings, w.String())
		}
	}

	logPhase("finalize", phaseStart)
	if profiler.Enabled && isTopLevel {
		fmt.Fprintf(os.Stderr, "  [analyze] %-35s %s\n", "TOTAL", time.Since(analyzeStart).Round(time.Millisecond))
	}

	return richAST, nil
}

// countErrors returns the number of Error-severity warnings in the list.
func countErrors(warnings []ValidationWarning) int {
	n := 0
	for _, w := range warnings {
		if w.Severity == SeverityError {
			n++
		}
	}
	return n
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
		Pos:        transpiler.PosFromToken(ctx.Identifier().GetStart()),
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
		pos    transpiler.SourcePos
		fields []variantFieldInfo
	}
	var variants []variantInfo

	// First pass: collect all variant fields
	allFieldTypes := make(map[string]map[string]bool) // field name -> set of type texts
	for _, caseCtx := range ctx.AllSealedCase() {
		sc := caseCtx.(*grammar.SealedCaseContext)
		variantName := sc.Identifier().GetText()
		vi := variantInfo{
			name: variantName,
			pos:  transpiler.PosFromToken(sc.Identifier().GetStart()),
		}

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

		// Warn if variant name collides with a std auto-imported companion name.
		// The variant creates a companion type that could shadow std companions
		// (e.g., Success, Failure, Some, None, Left, Right).
		if err := registry.CheckStdConflict(variantName, pkgName); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: sealed variant '%s' in type '%s' shadows %s; this may cause ambiguous symbols in generated Go code\n",
				variantName, typeName, err.Error())
		}
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
		sv := transpiler.SealedVariant{Name: vi.name, Pos: vi.pos}
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
			Pos:        vi.pos,
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

// findMatchingCloseBracket returns the index of the `]` that matches the `[`
// at position openIdx, respecting bracket nesting. Returns -1 if not found or
// if openIdx does not point at a `[`. Used by the map[K]V return-type parser
// to correctly split a key (which may itself be a generic type like `Pair[A,B]`)
// from the value.
func findMatchingCloseBracket(s string, openIdx int) int {
	if openIdx >= len(s) || s[openIdx] != '[' {
		return -1
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
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

	// Handle map types: map[K]V — split at the bracket that closes the key.
	// Must be recognized BEFORE the generic-type branch below, otherwise the
	// `map` token gets mistaken for a named type and picks up the current
	// package prefix (e.g., `collection_immutable.map`), corrupting downstream
	// type inference for `for k, v := range` over method-returned maps (gap #8).
	if strings.HasPrefix(typeName, "map[") {
		keyEnd := findMatchingCloseBracket(typeName, 3) // index of `[` after "map"
		if keyEnd != -1 && keyEnd+1 < len(typeName) {
			keyStr := typeName[4:keyEnd]
			valStr := typeName[keyEnd+1:]
			keyType := a.resolveTypeWithParams(keyStr, pkgName, typeParams)
			valType := a.resolveTypeWithParams(valStr, pkgName, typeParams)
			return transpiler.MapType{Key: keyType, Elem: valType}
		}
	}

	// If it's already package-qualified, handle it
	if strings.Contains(typeName, ".") {
		// For qualified generic types like lazy.Lazy[Array[rune]],
		// recursively resolve type arguments so inner types get proper prefixes
		if idx := strings.Index(typeName, "["); idx != -1 {
			baseQualified := typeName[:idx]
			baseType := transpiler.ParseType(baseQualified)

			_, argStrs := extractBaseAndArgs(typeName)
			var params []transpiler.Type
			for _, argStr := range argStrs {
				params = append(params, a.resolveTypeWithParams(strings.TrimSpace(argStr), pkgName, typeParams))
			}
			if len(params) > 0 {
				return transpiler.GenericType{Base: baseType, Params: params}
			}
		}
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

		// Resolve base type using shared resolver
		baseType := a.resolveBaseName(baseTypeName, pkgName)

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

	// Resolve non-generic base name using shared resolver
	return a.resolveBaseName(typeName, pkgName)
}

// scanImports processes GALA imports from a source file and loads their metadata
// into richAST. This is used to ensure sibling files' dependencies are available
// for type resolution during extractSiblingFullMetadata.
func (a *galaAnalyzer) scanImports(sf *grammar.SourceFileContext, richAST *transpiler.RichAST) {
	for _, impDecl := range sf.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			path := strings.Trim(s.STRING().GetText(), "\"")

			isInternalGala := strings.HasPrefix(path, "martianoff/gala/")
			isExternalGala := a.resolver.IsGalaPackage(path)

			if isInternalGala || isExternalGala {
				var relPath string
				if isInternalGala {
					relPath = strings.TrimPrefix(path, "martianoff/gala/")
				} else {
					relPath = path
				}

				if cached, ok := a.analyzedPkgs[path]; ok && cached != nil {
					richAST.Merge(cached)
					if cached.PackageName != "" && cached.PackageName != "main" && cached.PackageName != "test" {
						richAST.Packages[path] = cached.PackageName
					}
				} else if _, inProgress := a.analyzedPkgs[path]; !inProgress {
					a.analyzedPkgs[path] = nil
					if isExternalGala && !isInternalGala {
						if err := a.ensureTranspiled(path); err != nil {
							fmt.Fprintf(os.Stderr, "Warning: failed to transpile dependency %s: %v\n", path, err)
						}
					}
					importedAST, err := a.analyzePackage(relPath)
					if err == nil {
						a.analyzedPkgs[path] = importedAST
						richAST.Merge(importedAST)
						if importedAST.PackageName != "" && importedAST.PackageName != "main" && importedAST.PackageName != "test" {
							richAST.Packages[path] = importedAST.PackageName
						} else {
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
}

// findKnownTypePackage checks if a type name is already known in richAST.Types
// under a different package. This prevents incorrectly qualifying types from
// imported packages (e.g., Array from collection_immutable) with the current
// package name (e.g., server.Array).
//
// Returns the correct package name if found, or empty string if not.
func (a *galaAnalyzer) findKnownTypePackage(typeName string, currentPkg string) string {
	if a.currentRichAST == nil || len(a.currentDotImportPkgs) == 0 {
		return ""
	}
	// First check if the type is already defined in the current package.
	// If so, the current package qualification is correct - do not override.
	currentKey := currentPkg + "." + typeName
	if _, ok := a.currentRichAST.Types[currentKey]; ok {
		return ""
	}
	// Also check without package prefix (for main/test packages)
	if _, ok := a.currentRichAST.Types[typeName]; ok {
		return ""
	}
	// Check if the type exists in a dot-imported package.
	// Only dot-imported packages bring names into scope unqualified,
	// so only those should be considered for unqualified type resolution.
	for dotPkg := range a.currentDotImportPkgs {
		key := dotPkg + "." + typeName
		if _, ok := a.currentRichAST.Types[key]; ok {
			return dotPkg
		}
	}
	return ""
}

// resolveBaseName resolves a simple (unqualified, non-generic) type name to a transpiler.Type
// using the shared resolver.TypeResolver for consistent precedence with the transformer.
func (a *galaAnalyzer) resolveBaseName(typeName string, pkgName string) transpiler.Type {
	tr := a.buildTypeResolver(pkgName)
	exists := func(name string) bool {
		if a.currentRichAST == nil {
			return false
		}
		_, ok := a.currentRichAST.Types[name]
		return ok
	}

	if resolved, ok := tr.Resolve(typeName, exists); ok {
		// Parse the resolved name to extract package and type
		if dotIdx := strings.LastIndex(resolved, "."); dotIdx != -1 {
			return transpiler.NamedType{
				Package: resolved[:dotIdx],
				Name:    resolved[dotIdx+1:],
			}
		}
		return transpiler.BasicType{Name: resolved}
	}

	// Fallback: check std via registry (covers cases where type is known
	// to std but not yet in currentRichAST.Types, e.g., during initial analysis)
	if a.isStdType(typeName) {
		return transpiler.NamedType{Package: registry.StdPackageName, Name: typeName}
	}

	// Default to current package qualification for library packages
	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		return transpiler.NamedType{Package: pkgName, Name: typeName}
	}
	return transpiler.BasicType{Name: typeName}
}

// buildTypeResolver creates a resolver.TypeResolver from the analyzer's current state.
func (a *galaAnalyzer) buildTypeResolver(pkgName string) *resolver.TypeResolver {
	var imports []resolver.PackageInfo
	if a.currentDotImportPkgs != nil {
		for dotPkg := range a.currentDotImportPkgs {
			imports = append(imports, resolver.PackageInfo{
				PkgName: dotPkg,
				IsDot:   true,
			})
		}
	}
	return &resolver.TypeResolver{
		PackageName: pkgName,
		Imports:     imports,
	}
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
	var pkgStart time.Time
	if profiler.Enabled {
		pkgStart = time.Now()
		defer func() {
			fmt.Fprintf(os.Stderr, "    [analyzePackage] %-30s %s\n", relPath, time.Since(pkgStart).Round(time.Millisecond))
		}()
	}

	// Save and clear packageFiles to prevent them from interfering with recursive
	// Analyze calls. packageFiles are specific to the current compilation unit's package
	// and must not be applied when analyzing other packages (e.g., std).
	savedPackageFiles := a.packageFiles
	a.packageFiles = nil
	defer func() { a.packageFiles = savedPackageFiles }()

	// Use the resolver to find the package directory
	dirPath, err := a.resolver.ResolvePackagePath(relPath)
	if err != nil {
		return nil, fmt.Errorf("package not found: %s", relPath)
	}

	// Check disk cache before doing expensive analysis.
	// The cache key combines the package's own content hash with a hash of its
	// import paths (dependency identity). This ensures the cache invalidates when:
	// 1. Any source file in the package changes (contentHash)
	// 2. The set of imports changes (depsHash identity)
	// 3. Any imported package's content changes (depsHash includes resolved dep hashes)
	contentHash := hashPackageDir(dirPath)
	depsHash := hashImportPaths(dirPath)
	if contentHash != "" && a.cache != nil {
		cacheStart := time.Now()
		if cached := a.cache.Get(relPath, contentHash, depsHash); cached != nil {
			logCache(true, relPath, time.Since(cacheStart))
			return cached, nil
		}
		logCache(false, relPath, time.Since(cacheStart))
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
		if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" && !strings.HasSuffix(f.Name(), "_test.gala") {
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
				// Canonicalize filePath once — isSameFile resolves symlinks and
				// runs os.Stat, which is too expensive to call per type.
				canonFile, _ := filepath.Abs(filePath)
				if real, err := filepath.EvalSymlinks(canonFile); err == nil {
					canonFile = real
				}
				canonCache := map[string]string{filePath: canonFile}
				sameAsFilePath := func(p string) bool {
					if p == "" {
						return false
					}
					if c, ok := canonCache[p]; ok {
						return c == canonFile
					}
					abs, _ := filepath.Abs(p)
					if real, err := filepath.EvalSymlinks(abs); err == nil {
						abs = real
					}
					canonCache[p] = abs
					return abs == canonFile
				}
				for typeName, newMeta := range res.Types {
					if !sameAsFilePath(newMeta.DefinedIn) {
						continue
					}
					if existingMeta, ok := pkgAST.Types[typeName]; ok {
						if hasTypeDefinition(existingMeta) && existingMeta.DefinedIn != "" && !sameAsFilePath(existingMeta.DefinedIn) {
							return nil, galaerr.NewCodedSemanticError(
								galaerr.CodeTypeRedefinition,
								newMeta.Pos.Line, newMeta.Pos.Column,
								fmt.Sprintf("type %q in package %q redefined (first defined in %s)", newMeta.Name, res.PackageName, existingMeta.DefinedIn),
								"remove the duplicate declaration or rename one of the types",
							)
						}
					}
				}
				pkgAST.Merge(res)
			}
		}
	}

	// Scan .go files for exported symbols and type information.
	hasGalaFiles := false
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" {
			hasGalaFiles = true
			break
		}
	}
	if !hasGalaFiles {
		// For Go-only packages, also extract exported symbol names for collision warnings.
		a.extractGoFileExports(files, dirPath, relPath, pkgAST)
	}
	// Always extract Go type information from .go files, even in mixed GALA+Go packages.
	// This ensures Go-defined functions and variables (e.g., concurrent.Spawn) are available
	// for type inference when GALA code calls them.
	goInfo := AnalyzeGoFiles(dirPath)
	if len(goInfo.Functions) > 0 || len(goInfo.Types) > 0 || len(goInfo.Variables) > 0 || len(goInfo.TypeAliases) > 0 {
		if pkgAST.GoTypeInfo == nil {
			pkgAST.GoTypeInfo = transpiler.NewGoTypeInfo()
		}
		pkgAST.GoTypeInfo.Merge(goInfo)
	}

	// Store in disk cache for future processes
	if contentHash != "" && a.cache != nil {
		a.cache.Put(relPath, contentHash, depsHash, pkgAST)
	}

	return pkgAST, nil
}

// hasTypeDefinition returns true if the TypeMetadata represents a full type definition
// (struct with fields or sealed type with variants), as opposed to a type entry that
// only has methods added from another file.
func hasTypeDefinition(meta *transpiler.TypeMetadata) bool {
	return len(meta.FieldNames) > 0 || (meta.IsSealed && len(meta.SealedVariants) > 0)
}

// isSameFile checks whether two paths refer to the same file.
// Handles relative/absolute paths and Bazel symlinks on Linux.
func isSameFile(pathA, pathB string) bool {
	if pathA == "" || pathB == "" {
		return false
	}
	absA, err := filepath.Abs(pathA)
	if err != nil {
		absA = pathA
	}
	absB, err := filepath.Abs(pathB)
	if err != nil {
		absB = pathB
	}
	// Fast path: string comparison after Abs
	if absA == absB {
		return true
	}
	// Resolve symlinks (critical for Bazel local_path_override on Linux where
	// the same file is reachable via symlinked and real paths)
	realA, errA := filepath.EvalSymlinks(absA)
	realB, errB := filepath.EvalSymlinks(absB)
	if errA == nil && errB == nil {
		return realA == realB
	}
	// Fallback: use os.SameFile which compares inodes (works across symlinks)
	infoA, errA := os.Stat(absA)
	infoB, errB := os.Stat(absB)
	if errA == nil && errB == nil {
		return os.SameFile(infoA, infoB)
	}
	return false
}

// canonicalPath returns a canonical path by resolving symlinks and making absolute.
// Falls back to filepath.Abs if symlinks can't be resolved.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return real
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
			checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
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
// This includes struct fields, sealed types, shorthand structs, and all method/function
// signatures. Used for both --package-files mode and directory-discovered siblings
// to enable full cross-file type resolution.
func (a *galaAnalyzer) extractSiblingFullMetadata(sibTree *grammar.SourceFileContext, pkgName string, richAST *transpiler.RichAST, sibFilePath string) error {
	absSibPath, _ := filepath.Abs(sibFilePath)
	// 1. Collect struct types with full field info
	for _, topDecl := range sibTree.AllTopLevelDeclaration() {
		if typeDecl := topDecl.TypeDeclaration(); typeDecl != nil {
			ctx := typeDecl.(*grammar.TypeDeclarationContext)
			typeName := ctx.Identifier().GetText()
			fullTypeName := typeName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				fullTypeName = pkgName + "." + typeName
			}
			// Error if type already has field info from a different file — redefinition.
			// Skip if existing type was loaded from this same sibling file (via auto-import).
			// Skip if DefinedIn is empty — the type came from cache and should be overwritable.
			if existing, ok := richAST.Types[fullTypeName]; ok && len(existing.FieldNames) > 0 {
				if existing.DefinedIn != "" && !isSameFile(existing.DefinedIn, absSibPath) {
					return galaerr.NewCodedSemanticError(
						galaerr.CodeTypeRedefinition,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						fmt.Sprintf("type %q in package %q redefined (first defined in %s)", typeName, pkgName, existing.DefinedIn),
						"remove the duplicate declaration or rename one of the types",
					)
				}
				if existing.DefinedIn != "" {
					continue
				}
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
				Pos:     transpiler.PosFromToken(ctx.Identifier().GetStart()),
				Methods: existingMethods,
				Fields:  make(map[string]transpiler.Type),
			}
			// Preserve DefinedIn from main analysis if already set
			if ex, ok := richAST.Types[fullTypeName]; ok && ex.DefinedIn != "" {
				meta.DefinedIn = ex.DefinedIn
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
					if meta.FieldPositions == nil {
						meta.FieldPositions = make(map[string]transpiler.SourcePos)
					}
					meta.FieldPositions[fieldName] = transpiler.PosFromToken(fctx.Identifier().GetStart())
				}
				if meta.DefinedIn == "" {
					meta.DefinedIn = absSibPath
				}
			}
			if ctx.InterfaceType() != nil {
				ifaceType := ctx.InterfaceType().(*grammar.InterfaceTypeContext)
				for _, ms := range ifaceType.AllMethodSpec() {
					msCtx := ms.(*grammar.MethodSpecContext)
					methodName := msCtx.Identifier().GetText()
					methodMeta := &transpiler.MethodMetadata{
						Name:      methodName,
						Package:   pkgName,
						Pos:       transpiler.PosFromToken(msCtx.Identifier().GetStart()),
						DefinedIn: absSibPath,
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
			// Extract type aliases (e.g., type Handler func(Request) Future[Response])
			if ctx.TypeAlias() != nil {
				aliasCtx := ctx.TypeAlias().(*grammar.TypeAliasContext)
				if aliasCtx.Type_() != nil {
					underlyingType := a.resolveTypeWithParams(aliasCtx.Type_().GetText(), pkgName, meta.TypeParams)
					if !underlyingType.IsNil() {
						if richAST.TypeAliases == nil {
							richAST.TypeAliases = make(map[string]transpiler.Type)
						}
						richAST.TypeAliases[typeName] = underlyingType
					}
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
				if existing.DefinedIn != "" && !isSameFile(existing.DefinedIn, absSibPath) {
					return galaerr.NewCodedSemanticError(
						galaerr.CodeTypeRedefinition,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						fmt.Sprintf("type %q in package %q redefined (first defined in %s)", typeName, pkgName, existing.DefinedIn),
						"remove the duplicate declaration or rename one of the types",
					)
				}
				if existing.DefinedIn != "" {
					continue
				}
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
				Pos:     transpiler.PosFromToken(ctx.Identifier().GetStart()),
				Methods: existingMethods,
				Fields:  make(map[string]transpiler.Type),
			}
			// Preserve DefinedIn from main analysis if already set
			if ex, ok := richAST.Types[fullTypeName]; ok && ex.DefinedIn != "" {
				meta.DefinedIn = ex.DefinedIn
			}
			if meta.DefinedIn == "" {
				meta.DefinedIn = absSibPath
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
						if meta.FieldPositions == nil {
							meta.FieldPositions = make(map[string]transpiler.SourcePos)
						}
						meta.FieldPositions[fieldName] = transpiler.PosFromToken(pctx.Identifier().GetStart())
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
			// Error if sealed type already defined in a different file — redefinition.
			// Skip if loaded from this same sibling file (via auto-import).
			// Skip if DefinedIn is empty — the type came from cache.
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.IsSealed {
				if existing.DefinedIn != "" && !isSameFile(existing.DefinedIn, absSibPath) {
					return galaerr.NewCodedSemanticError(
						galaerr.CodeTypeRedefinition,
						ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
						fmt.Sprintf("type %q in package %q redefined (first defined in %s)", typeName, pkgName, existing.DefinedIn),
						"remove the duplicate declaration or rename one of the types",
					)
				}
				if existing.DefinedIn != "" {
					continue
				}
			}
			a.analyzeSealedType(ctx, pkgName, richAST)
			// Set DefinedIn on the parent and all companion variants (only when empty).
			if meta, ok := richAST.Types[fullTypeName]; ok {
				if meta.DefinedIn == "" {
					meta.DefinedIn = absSibPath
				}
				for _, v := range meta.SealedVariants {
					companionKey := v.Name
					if pkgName != "" && pkgName != "main" && pkgName != "test" {
						companionKey = pkgName + "." + v.Name
					}
					if cm, ok := richAST.Types[companionKey]; ok && cm.DefinedIn == "" {
						cm.DefinedIn = absSibPath
					}
				}
			}
		}
	}

	// 3. Collect methods and functions
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
					Name:         methodName,
					Package:      pkgName,
					Pos:          transpiler.PosFromToken(ctx.Identifier().GetStart()),
					ReceiverName: recvCtx.Identifier().GetText(),
					DefinedIn:    absSibPath,
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
					// Mirror the section-2 IsGeneric detection so that sibling-extracted
					// methods carry the same function-form flag as methods analyzed
					// directly. Without this, merging a sibling's Analyze result (with
					// IsGeneric=true) on top of an earlier sibling's result (with
					// IsGeneric=false from extraction here) would clobber the true
					// flag and the transpiler would emit method-syntax calls against
					// a standalone-function definition.
					recvTypeStr := recvCtx.Type_().GetText()
					retTypeStr := ctx.Signature().Type_().GetText()
					if a.causesInstantiationCycle(recvTypeStr, retTypeStr) {
						methodMeta.IsGeneric = true
					}
				}
				if ctx.Signature().Parameters() != nil {
					pCtx := ctx.Signature().Parameters().(*grammar.ParametersContext)
					if pList := pCtx.ParameterList(); pList != nil {
						for i, p := range pList.(*grammar.ParameterListContext).AllParameter() {
							paramCtx := p.(*grammar.ParameterContext)
							if paramCtx.Type_() != nil {
								methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, allTypeParams))
							} else {
								methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
							}
							if paramCtx.Identifier() != nil {
								methodMeta.ParamNames = append(methodMeta.ParamNames, paramCtx.Identifier().GetText())
							} else {
								methodMeta.ParamNames = append(methodMeta.ParamNames, "")
							}
							// Extract default expression source text
							if paramCtx.ParamDefault() != nil {
								if methodMeta.DefaultExprs == nil {
									methodMeta.DefaultExprs = make(map[int]string)
								}
								defaultCtx := paramCtx.ParamDefault().(*grammar.ParamDefaultContext)
								methodMeta.DefaultExprs[i] = defaultCtx.Expression().GetText()
							}
						}
					}
				}

				if typeMeta, ok := richAST.Types[fullBaseType]; ok {
					if _, exists := typeMeta.Methods[methodName]; !exists {
						typeMeta.Methods[methodName] = methodMeta
					} else if !typeMeta.Methods[methodName].IsGeneric && methodMeta.IsGeneric {
						// Upgrade IsGeneric=false to true if sibling extraction
						// detects the instantiation-cycle pattern that section 2
						// may have missed (e.g. when the receiver type wasn't yet
						// populated in this file's richAST). IsGeneric is
						// append-only: once any pass sets it true, later passes
						// keep it true. Fixes cross-package stdlib generic method
						// dispatch (Array.ZipWithIndex, Array.Grouped, etc.).
						typeMeta.Methods[methodName].IsGeneric = true
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
						Name:      funcName,
						Package:   pkgName,
						Pos:       transpiler.PosFromToken(ctx.Identifier().GetStart()),
						DefinedIn: absSibPath,
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
							for i, p := range pList.(*grammar.ParameterListContext).AllParameter() {
								paramCtx := p.(*grammar.ParameterContext)
								if paramCtx.Type_() != nil {
									funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(paramCtx.Type_().GetText(), pkgName, funcMeta.TypeParams))
								} else {
									funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
								}
								if paramCtx.Identifier() != nil {
									funcMeta.ParamNames = append(funcMeta.ParamNames, paramCtx.Identifier().GetText())
								} else {
									funcMeta.ParamNames = append(funcMeta.ParamNames, "")
								}
								if paramCtx.ParamDefault() != nil {
									if funcMeta.DefaultExprs == nil {
										funcMeta.DefaultExprs = make(map[int]string)
									}
									defaultCtx := paramCtx.ParamDefault().(*grammar.ParamDefaultContext)
									funcMeta.DefaultExprs[i] = defaultCtx.Expression().GetText()
								}
							}
						}
					}
					richAST.Functions[fullFuncName] = funcMeta
				}
			}
		}
	}
	return nil
}


// validateDefaultParams checks that default parameter values follow the rules:
// 1. Parameters with defaults must come after all required parameters
// 2. Variadic parameters cannot have defaults
// 3. Default expression types must be compatible with parameter types (for literals)
func validateDefaultParams(funcMeta *transpiler.FunctionMetadata, line, column int, filePath string) error {
	_ = filePath // filePath is retained for future use; position info now travels via the coded error
	if len(funcMeta.DefaultExprs) == 0 {
		return nil
	}

	// Check ordering: once a default is seen, all subsequent params must have defaults (or be variadic)
	seenDefault := false
	for i := range funcMeta.ParamTypes {
		_, hasDefault := funcMeta.DefaultExprs[i]
		if hasDefault {
			seenDefault = true
		} else if seenDefault {
			paramName := ""
			if i < len(funcMeta.ParamNames) {
				paramName = funcMeta.ParamNames[i]
			}
			return galaerr.NewCodedSemanticError(
				galaerr.CodeParamMissingDefaultAfterDefault,
				line, column,
				fmt.Sprintf("parameter %q in %s has no default but follows a parameter with a default", paramName, funcMeta.Name),
				"move parameters with defaults to the end of the parameter list",
			)
		}
	}

	// Check literal type compatibility
	for i, defaultText := range funcMeta.DefaultExprs {
		if i >= len(funcMeta.ParamTypes) {
			continue
		}
		paramType := funcMeta.ParamTypes[i].String()
		if paramType == "" || paramType == "<nil>" {
			continue
		}
		literalType := inferLiteralType(defaultText)
		if literalType == "" {
			continue // non-literal expression, can't validate statically
		}
		if !typesCompatibleForDefault(paramType, literalType) {
			paramName := ""
			if i < len(funcMeta.ParamNames) {
				paramName = funcMeta.ParamNames[i]
			}
			return galaerr.NewCodedSemanticError(
				galaerr.CodeParamDefaultTypeMismatch,
				line, column,
				fmt.Sprintf("default for parameter %q has type %s, expected %s", paramName, literalType, paramType),
				"fix the default expression or change the parameter type",
			)
		}
	}

	return nil
}

// inferLiteralType returns the type of a literal expression, or "" if not a literal.
func inferLiteralType(expr string) string {
	if expr == "true" || expr == "false" {
		return "bool"
	}
	if expr == "nil" {
		return "" // nil is compatible with many types
	}
	if len(expr) >= 2 && expr[0] == '"' && expr[len(expr)-1] == '"' {
		return "string"
	}
	if len(expr) >= 2 && expr[0] == '\'' && expr[len(expr)-1] == '\'' {
		return "rune"
	}
	// Check for float (contains . or e/E)
	if isNumericLiteral(expr) {
		for _, c := range expr {
			if c == '.' || c == 'e' || c == 'E' {
				return "float64"
			}
		}
		return "int"
	}
	return ""
}

// isNumericLiteral checks if a string looks like a numeric literal.
func isNumericLiteral(s string) bool {
	if len(s) == 0 {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	hasDigit := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			hasDigit = true
		} else if c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			// valid in float literals
		} else {
			return false
		}
	}
	return hasDigit
}

// typesCompatibleForDefault checks if a literal type is compatible with a parameter type.
func typesCompatibleForDefault(paramType, literalType string) bool {
	if paramType == literalType {
		return true
	}
	// Numeric widening: int literal can be used for int, int8, int16, int32, int64, uint*, float*
	if literalType == "int" {
		switch paramType {
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "byte", "rune":
			return true
		}
	}
	// Float literal can be used for float32, float64
	if literalType == "float64" {
		switch paramType {
		case "float32", "float64":
			return true
		}
	}
	// any accepts everything
	if paramType == "any" || paramType == "interface{}" {
		return true
	}
	return false
}

var _ transpiler.Analyzer = (*galaAnalyzer)(nil)

// lookupSiblingCache returns parsed sibling trees from the in-memory
// cache, filtered for the current file. Returns (nil, nil, nil) on a
// cache miss so the caller can fall through to a fresh scan. Returns
// (nil, nil, err) when a cached entry surfaces a still-relevant
// GALA-E0010 package-name mismatch; the caller propagates the error.
//
// Validity check: compare the cached dirSize (.gala file count at
// capture time) against the current ReadDir count. A mismatch drops
// the entry as stale. This is cheap compared to re-parsing and covers
// the common mutation modes (file added/removed).
func (a *galaAnalyzer) lookupSiblingCache(canonDir, dirPath, filePath, pkgName string) ([]*grammar.SourceFileContext, []string, error) {
	entry, ok := a.siblingTreeCache[canonDir]
	if !ok {
		return nil, nil, nil
	}
	// Revalidate: if the directory's .gala count changed, invalidate.
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, nil, nil
	}
	count := 0
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" {
			count++
		}
	}
	if count != entry.dirSize {
		delete(a.siblingTreeCache, canonDir)
		delete(a.checkedDirs, canonDir)
		return nil, nil, nil
	}
	return a.filterSiblingsForCurrentFile(entry.trees, entry.paths, filePath, pkgName, dirPath)
}

// filterSiblingsForCurrentFile applies per-Analyze filters to a set of
// pre-parsed sibling trees: excludes the current file (via symlink-aware
// isSameFile), enforces the package-name contract (with Go-style
// `_test.gala` relaxation), and drops test files from non-test callers.
//
// Mirrors the filtering logic that previously lived inline in the
// directory-discovery loop; extracting it lets both the cache-hit path
// and the cache-miss path share the same rules.
func (a *galaAnalyzer) filterSiblingsForCurrentFile(
	trees []*grammar.SourceFileContext,
	paths []string,
	filePath, pkgName, dirPath string,
) ([]*grammar.SourceFileContext, []string, error) {
	if len(trees) == 0 {
		return nil, nil, nil
	}
	currentIsTest := strings.HasSuffix(filePath, "_test.gala")
	var outTrees []*grammar.SourceFileContext
	var outPaths []string
	for i, tree := range trees {
		path := paths[i]
		if isSameFile(path, filePath) {
			continue
		}
		pkgClause := tree.PackageClause().(*grammar.PackageClauseContext)
		otherPkgName := pkgClause.Identifier().GetText()
		siblingIsTest := strings.HasSuffix(path, "_test.gala")
		if otherPkgName != pkgName && !(siblingIsTest || currentIsTest) {
			return nil, nil, galaerr.NewCodedSemanticError(
				galaerr.CodeDuplicatePackageName,
				pkgClause.GetStart().GetLine(), pkgClause.GetStart().GetColumn(),
				fmt.Sprintf("directory %s has files with different package names: %q and %q", dirPath, pkgName, otherPkgName),
				"use the same package name across all sibling .gala files, or move the file to a different directory",
			)
		}
		if otherPkgName == pkgName && (!siblingIsTest || currentIsTest) {
			outTrees = append(outTrees, tree)
			outPaths = append(outPaths, path)
		}
	}
	return outTrees, outPaths, nil
}

