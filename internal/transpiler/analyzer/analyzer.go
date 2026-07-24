package analyzer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	// analyzedPkgs caches per-package metadata across files in a batch.
	// Each entry holds an OWN-ONLY projection of the package's RichAST
	// (Types/Functions/methods/etc. originating in that package). The
	// transitive closure required by a downstream consumer is reconstructed
	// at read time by recursively merging direct-import entries via
	// analyzedPkgImports — see mergeAnalyzedClosureAt. This bounds the
	// in-memory size of the batch cache to O(packages × own_metadata)
	// instead of O(packages × full_closure), which is the in-memory
	// counterpart to PR #308's on-disk cache projection.
	analyzedPkgs        map[string]*transpiler.RichAST // Cache of analyzed packages (own-only projections)
	analyzedPkgImports  map[string][]string            // path -> direct GALA import paths (for closure rehydration)
	checkedDirs  map[string]bool
	resolver            *module.Resolver               // Handles module root discovery and package path resolution
	currentRichAST      *transpiler.RichAST            // Set during Analyze() for cross-reference in resolveTypeWithParams
	currentDotImportPkgs map[string]bool                // Package names that are dot-imported in the current file
	currentNamedImportPkgs map[string]bool              // Package names imported with a non-dot import in the current file (e.g. `import "pkg/x"` or `import al "pkg/x"`)
	currentExplicitImportPaths map[string]bool          // Full GALA import paths the current file declared (covers BOTH dot and named imports — used to scope unresolved-symbol errors to symbols that *should* have been imported)
	analyzeDepth int                                    // recursion depth for profiling
	cache        *analysisCache                         // disk-based package analysis cache

	// pendingResolveErrors collects GALA-E0025 errors for unqualified
	// names that don't resolve through the current file's explicit
	// imports + std prelude + current package. Drained at the end of
	// each top-level Analyze (analyzeDepth == 1) so the user gets a
	// concrete error rather than the previous silent "default to
	// current package" mis-qualification. Never carries across
	// recursive analyzePackage boundaries — each sibling/dependency
	// analysis collects its own list.
	pendingResolveErrors []*galaerr.SemanticError

	// P1 (perf): per-analyzer in-memory cache of parsed sibling ASTs.
	// Key is the canonical directory path; value holds the trees and paths
	// captured on the first scan. Reused across Analyze() calls within the
	// same process so a 5-file package does not re-read and re-parse its
	// siblings 5 times — the dominant cost in the analyze phase (86.9% of
	// total transpile time for collection_immutable/list.gala, baseline
	// 2.6s on Windows).
	siblingTreeCache map[string]*siblingCacheEntry

	// Per-file parse cache used by the explicit-package-files branch (and
	// reusable by anyone else that wants to load + parse a .gala by path).
	// Keyed by canonical absolute path; invalidated by mtime+size mismatch.
	// Survives BatchAnalyzer.SetPackageFiles so a 37-file package amortizes
	// sibling parsing to roughly N parses instead of N×(N−1).
	//
	// parsedFileCacheMu guards parsedFileCache against concurrent
	// writes. Pointer type (not embedded value) so that a child
	// analyzer constructed with a shared parsedFileCache can also
	// share the mutex — otherwise a copied value-typed mutex would
	// give the child its own lock and let goroutines from parent and
	// child write to the same map under different locks.
	parsedFileCache   map[string]*parsedFileEntry
	parsedFileCacheMu *sync.Mutex

	// Per-analyzer in-memory cache of analyzePackage results, keyed by the
	// resolved package directory path. The same package directory is
	// frequently reached through several different import paths during the
	// closure walk on apex transpiles (e.g. martianoff/gala/std appears in
	// every file's import list AND inside every transitive dep's import
	// list). Without this layer, each visit re-runs hashPackageDir +
	// cache.Get + gob.Decode + rehydrateImports — a ~50 ms-per-package
	// disk-bound dance that on ubuntu-latest CI runners stretches to the
	// hundreds of seconds for a single apex action. The memoization makes
	// the work O(unique-packages) per worker process instead of
	// O(visits-during-closure-walk).
	pkgResultCache map[string]*pkgResultCacheEntry

	// When true, ensureTranspiled is a no-op — used by the LSP, where the
	// generated .gen.go files are never consumed (analyzePackage reads .gala
	// directly to extract the metadata diagnostics need). The disk write is
	// dead work in LSP context and contends heavily under parallel analyzers
	// on Windows.
	skipTranspileToDisk bool

	// goSrcDirs maps a Go import-path prefix (a module path, or an exact
	// package path) to the on-disk directory holding that package's .go
	// source. It is the module-aware escape hatch for third-party Go
	// interop: go/importer's "source" mode (AnalyzeGoPackage) is
	// GOPATH/GOROOT-based and cannot resolve a versioned module from the
	// module cache (CLI) or Bazel's external tree, so it returns nothing for
	// e.g. github.com/google/uuid. When the importer comes up empty, the
	// analyzer falls back to parsing the package's real .go sources from the
	// directory resolved here (the same code path that already resolves
	// hand-written local .go files). Populated by the builder (from gala.mod
	// + GOMODCACHE) and by the worker's --go-src flag (from the Bazel rule).
	// Nil when no Go module sources are wired in (stdlib-only projects).
	goSrcDirs map[string]string
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

// pkgResultCacheEntry is one slot in galaAnalyzer.pkgResultCache. We
// stash the fully-analyzed RichAST so a subsequent analyzePackage call
// for the same directory short-circuits before doing any disk work,
// AND we stash the directImports list so the closure walker that
// already lives on the warm-cache path keeps functioning. Cached
// entries are valid for the lifetime of the worker process: source
// files don't change during a single Bazel build, and the cache is
// dropped when the BatchAnalyzer is replaced.
type pkgResultCacheEntry struct {
	pkgAST        *transpiler.RichAST
	directImports []string
}

// parsedFileEntry is one slot in galaAnalyzer.parsedFileCache. The
// (mtime, size) pair is the staleness check: if either changes between
// stat calls, the cached tree is dropped and the file is re-parsed.
type parsedFileEntry struct {
	tree    *grammar.SourceFileContext
	pkgName string
	mtime   time.Time
	size    int64
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
		parser:           p,
		searchPaths:      searchPaths,
		analyzedPkgs:        make(map[string]*transpiler.RichAST),
		analyzedPkgImports:  make(map[string][]string),
		checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
		parsedFileCache:   make(map[string]*parsedFileEntry),
		parsedFileCacheMu: &sync.Mutex{},
		pkgResultCache:  make(map[string]*pkgResultCacheEntry),
		resolver:         module.NewResolver(searchPaths),
		cache:            newAnalysisCache(resolveCacheRoot(root)),
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
		parser:           p,
		searchPaths:      searchPaths,
		packageFiles:     packageFiles,
		analyzedPkgs:        make(map[string]*transpiler.RichAST),
		analyzedPkgImports:  make(map[string][]string),
		checkedDirs:      make(map[string]bool),
		siblingTreeCache: make(map[string]*siblingCacheEntry),
		parsedFileCache:   make(map[string]*parsedFileEntry),
		parsedFileCacheMu: &sync.Mutex{},
		pkgResultCache:  make(map[string]*pkgResultCacheEntry),
		resolver:         module.NewResolver(searchPaths),
		cache:            newAnalysisCache(resolveCacheRoot(root)),
	}
}

// NewGalaAnalyzerForLSP creates an analyzer configured for LSP use: the heavy
// disk-writing transpile-on-import side effect (ensureTranspiled) is disabled
// because its output (.gen.go files) is never consumed by the LSP pipeline.
// analyzePackage produces all the metadata diagnostics need directly from
// .gala sources. See the docstring on galaAnalyzer.skipTranspileToDisk and
// the no-op guard in ensureTranspiled for details.
func NewGalaAnalyzerForLSP(p transpiler.GalaParser, searchPaths []string, projectRoot ...string) transpiler.Analyzer {
	root := ""
	if len(projectRoot) > 0 {
		root = projectRoot[0]
	}
	return &galaAnalyzer{
		parser:              p,
		searchPaths:         searchPaths,
		analyzedPkgs:        make(map[string]*transpiler.RichAST),
		analyzedPkgImports:  make(map[string][]string),
		checkedDirs:         make(map[string]bool),
		siblingTreeCache:    make(map[string]*siblingCacheEntry),
		parsedFileCache:     make(map[string]*parsedFileEntry),
		parsedFileCacheMu:   &sync.Mutex{},
		pkgResultCache:     make(map[string]*pkgResultCacheEntry),
		resolver:            module.NewResolver(searchPaths),
		cache:               newAnalysisCache(resolveCacheRoot(root)),
		skipTranspileToDisk: true,
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
			parser:             p,
			searchPaths:        searchPaths,
			analyzedPkgs:       make(map[string]*transpiler.RichAST),
			analyzedPkgImports: make(map[string][]string),
			checkedDirs:        make(map[string]bool),
			siblingTreeCache:   make(map[string]*siblingCacheEntry),
			parsedFileCache:    make(map[string]*parsedFileEntry),
			parsedFileCacheMu:  &sync.Mutex{},
		pkgResultCache:    make(map[string]*pkgResultCacheEntry),
			resolver:           module.NewResolver(searchPaths),
			cache:              newAnalysisCache(resolveCacheRoot(root)),
		},
	}
}

// SetPackageFiles configures the sibling files for the next Analyze call.
// Also resets checkedDirs so directory-based sibling discovery works fresh per file.
func (b *BatchAnalyzer) SetPackageFiles(files []string) {
	b.inner.packageFiles = files
	b.inner.checkedDirs = make(map[string]bool)
}

// SetGoSrcDirs wires module-aware Go source directories into the inner
// analyzer (see galaAnalyzer.goSrcDirs). Idempotent and invariant for a build,
// so callers set it once after construction.
func (b *BatchAnalyzer) SetGoSrcDirs(dirs map[string]string) {
	b.inner.goSrcDirs = dirs
}

// Analyze delegates to the inner analyzer, sharing the package cache.
func (b *BatchAnalyzer) Analyze(tree antlr.Tree, filePath string) (*transpiler.RichAST, error) {
	return b.inner.Analyze(tree, filePath)
}

// SetGoSrcDirs wires module-aware Go source directories into the analyzer
// (see galaAnalyzer.goSrcDirs). Used by the single-file/LSP analyzers that
// are not BatchAnalyzers.
func (a *galaAnalyzer) SetGoSrcDirs(dirs map[string]string) {
	a.goSrcDirs = dirs
}

// resolveGoSrcDir maps a Go import path to its on-disk .go source directory
// using the wired goSrcDirs table. It first tries an exact match, then the
// longest registered import-path prefix (a module path), appending the
// remaining package subpath. Returns ("", false) when nothing is wired or
// no prefix matches — in which case the caller leaves Go type resolution to
// go/importer (correct for stdlib, which is always on GOROOT).
func (a *galaAnalyzer) resolveGoSrcDir(importPath string) (string, bool) {
	return ResolveGoSrcDir(a.goSrcDirs, importPath)
}

// ResolveGoSrcDir maps a Go import path to its on-disk .go source directory
// using the given module-path -> directory table. It first tries an exact
// match, then the longest registered import-path prefix (a module path),
// appending the remaining package subpath. Returns ("", false) when nothing is
// wired or no prefix matches. Exported so the LSP can resolve third-party Go
// source directories for go-to-definition using the same table the analyzer
// uses for type inference.
func ResolveGoSrcDir(goSrcDirs map[string]string, importPath string) (string, bool) {
	if len(goSrcDirs) == 0 {
		return "", false
	}
	if dir, ok := goSrcDirs[importPath]; ok {
		return dir, true
	}
	bestKey := ""
	for k := range goSrcDirs {
		if len(k) > len(bestKey) && strings.HasPrefix(importPath, k+"/") {
			bestKey = k
		}
	}
	if bestKey == "" {
		return "", false
	}
	rel := strings.TrimPrefix(importPath, bestKey+"/")
	return filepath.Join(goSrcDirs[bestKey], filepath.FromSlash(rel)), true
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
		// Explicit package files: parse each one (with cache), validate
		// package name, add to siblings. The cache is content-addressed by
		// (path, mtime, size) and survives across SetPackageFiles calls so
		// a 37-file package amortizes sibling parsing across the batch.
		//
		// Parses run concurrently: the cold-path bottleneck on Bazel
		// batch transpiles is parsing this set sequentially when the
		// parsedFileCache is empty (first request in a worker session).
		// parseFilesConcurrent dispatches at most GOMAXPROCS goroutines
		// and routes each through parseFileCached, so a sibling already
		// parsed by a previous SetPackageFiles call is not re-parsed.
		toParse := make([]string, 0, len(a.packageFiles))
		for _, pf := range a.packageFiles {
			if isSameFile(pf, filePath) {
				continue // skip self (resolves symlinks for Bazel on Linux)
			}
			toParse = append(toParse, pf)
		}
		trees := a.parseFilesConcurrent(toParse)
		for i, otherSF := range trees {
			if otherSF == nil {
				continue
			}
			pf := toParse[i]
			pkgClause, ok := otherSF.PackageClause().(*grammar.PackageClauseContext)
			if !ok || pkgClause.Identifier() == nil {
				continue
			}
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
				// Collect all candidate paths first so the parses can run
				// concurrently. The previous shape parsed siblings one at
				// a time; for a 5-file package like collection_immutable
				// that single loop dominated the cold-path analyze phase
				// (2.4 s / 4.6 s wall on Windows; the equivalent share on
				// CI is the largest contributor to the per-package
				// transpile time the perf-real-build workflow polices).
				var candidatePaths []string
				galaFileCount := 0
				for _, f := range files {
					if f.IsDir() || filepath.Ext(f.Name()) != ".gala" {
						continue
					}
					galaFileCount++
					candidatePaths = append(candidatePaths, filepath.Join(dirPath, f.Name()))
				}
				// parseFilesConcurrent populates parsedFileCache through
				// parseFileCached, so a sibling already parsed by a prior
				// Analyze on the same BatchAnalyzer is not re-parsed. Per
				// the function's docstring, ANTLR's runtime is thread-safe
				// and each Parse call constructs its own lexer+parser, so
				// the only shared state is the global DFA which the antlr
				// runtime guards internally.
				trees := a.parseFilesConcurrent(candidatePaths)
				var cacheTrees []*grammar.SourceFileContext
				var cachePaths []string
				for i, otherSF := range trees {
					if otherSF == nil {
						continue
					}
					cacheTrees = append(cacheTrees, otherSF)
					cachePaths = append(cachePaths, candidatePaths[i])
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
	// mergeVisited tracks which analyzedPkgs entries we've already merged
	// into richAST during this Analyze call, so each closure walk skips
	// shared-dependency subtrees (e.g., std appearing inside every import)
	// that the previous walk already brought in. This is a pure perf knob —
	// RichAST.Merge is idempotent (the COW guard at transpiler.go:124 makes
	// repeat merges no-ops for already-present types), so missing this
	// dedupe wastes cycles but never produces wrong results.
	mergeVisited := make(map[string]bool)

	// 0.25 Load std package metadata
	// For non-std packages: add as implicit import
	// For std package: still load for intra-package type resolution, but don't add to Packages
	if cachedStd, ok := a.analyzedPkgs[registry.StdImportPath]; ok && cachedStd != nil {
		// Use cached std metadata — walk its closure to materialize transitive
		// types (analyzedPkgs entries are own-only projections; see
		// projectOwnRichAST + mergeAnalyzedClosureAt for rationale).
		a.mergeAnalyzedClosureAt(richAST, registry.StdImportPath, mergeVisited)
		if pkgName != registry.StdPackageName {
			richAST.Packages[registry.StdImportPath] = registry.StdPackageName
		}
	} else if _, inProgress := a.analyzedPkgs[registry.StdImportPath]; !inProgress {
		// First time analyzing std - set placeholder to prevent infinite recursion
		a.analyzedPkgs[registry.StdImportPath] = nil
		stdAST, err := a.analyzePackage(registry.StdPackageName)
		if err == nil {
			a.storeAnalyzedPkg(registry.StdImportPath, stdAST)
			a.mergeAnalyzedClosureAt(richAST, registry.StdImportPath, mergeVisited)
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
					// Use cached metadata — walk closure to materialize transitive types.
					a.mergeAnalyzedClosureAt(richAST, path, mergeVisited)
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
						a.storeAnalyzedPkg(path, importedAST)
						a.mergeAnalyzedClosureAt(richAST, path, mergeVisited)
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

			// This is a Go package — analyze it for type info.
			goInfo := AnalyzeGoPackage(path)
			// go/importer "source" mode resolves Go stdlib (always on
			// GOROOT/src) but cannot find a versioned third-party module in
			// the module cache or Bazel's external tree, so it returns
			// nothing for e.g. github.com/google/uuid. When that happens and
			// the package's real .go source directory has been wired in (CLI
			// builder from gala.mod+GOMODCACHE, or the worker's --go-src flag
			// under Bazel), parse it directly — the same path that already
			// resolves hand-written local .go files, yielding concrete types
			// instead of falling back to `any`.
			if !goTypeInfoNonEmpty(goInfo) {
				if dir, ok := a.resolveGoSrcDir(path); ok {
					if srcInfo := AnalyzeGoFiles(dir); goTypeInfoNonEmpty(srcInfo) {
						goInfo = srcInfo
					}
				}
			}
			if goTypeInfoNonEmpty(goInfo) {
				if richAST.GoTypeInfo == nil {
					richAST.GoTypeInfo = transpiler.NewGoTypeInfo()
				}
				richAST.GoTypeInfo.Merge(goInfo)
			}
		}
	}

	logPhase("analyze-go-packages", phaseStart)
	phaseStart = time.Now()

	// 0.65 Scan hand-written .go files in the main file's directory.
	// When a GALA library coexists with hand-written Go in the same package
	// (e.g., `event.go` declaring `Event` and `Make() []Event` next to a
	// `pipe.gala` that consumes them), the Go-declared functions and types
	// are not reachable through any import path — they're in the local
	// package. Without this scan, calls like `Make()` from GALA resolve to
	// `NilType` and downstream type inference (e.g., `ArrayFromSlice(Make())`
	// resolving its `T` from `[]Event`) silently fails, leaving lambda
	// parameter types as the un-substituted type-parameter name.
	if filePath != "" && pkgName != "main" && pkgName != "test" {
		dirPath := filepath.Dir(filePath)
		goInfo := AnalyzeGoFiles(dirPath)
		if len(goInfo.Functions) > 0 || len(goInfo.Types) > 0 || len(goInfo.Variables) > 0 || len(goInfo.TypeAliases) > 0 {
			if richAST.GoTypeInfo == nil {
				richAST.GoTypeInfo = transpiler.NewGoTypeInfo()
			}
			richAST.GoTypeInfo.Merge(goInfo)
		}
	}

	logPhase("analyze-local-go-files", phaseStart)
	phaseStart = time.Now()

	// 0.75 Also scan sibling imports to ensure all GALA packages used by siblings
	// are loaded into richAST.Types. Without this, resolveTypeWithParams for sibling
	// struct fields can't find types from packages that only siblings import.
	for _, sibTree := range siblingTrees {
		a.scanImports(sibTree, richAST, mergeVisited)
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

	// Build the THIS-FILE explicit-import package set (path → pkgName).
	// Used by the unresolved-cross-package validation pass after analysis:
	// a name like `Array` that resolves to `collection_immutable.Array` is
	// only accepted if *this file* explicitly imported
	// `martianoff/gala/collection_immutable` (dot or named). Sibling
	// imports do NOT propagate — that's the GALA-E0025 contract.
	explicitImportPkgs := make(map[string]bool)
	explicitImportPkgs[registry.StdPackageName] = true // std prelude
	explicitImportPkgs[pkgName] = true                 // self
	for _, impDecl := range sourceFile.AllImportDeclaration() {
		ctx := impDecl.(*grammar.ImportDeclarationContext)
		for _, spec := range ctx.AllImportSpec() {
			s := spec.(*grammar.ImportSpecContext)
			path := strings.Trim(s.STRING().GetText(), "\"")
			if pname, ok := richAST.Packages[path]; ok && pname != "" {
				explicitImportPkgs[pname] = true
			}
		}
	}

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
					// Reject duplicate field names within a single struct
					// declaration. Without this, the second declaration
					// silently overwrote the first in the Fields map and
					// duplicated the name in FieldNames, producing invalid
					// Go output. This guard is scoped to one parse of one
					// struct (Fields was cleared on re-analysis at line ~740),
					// so it does not need an existing.DefinedIn check.
					if _, exists := meta.Fields[fieldName]; exists {
						return nil, galaerr.NewCodedSemanticError(
							galaerr.CodeStructFieldRedeclared,
							fctx.Identifier().GetStart().GetLine(), fctx.Identifier().GetStart().GetColumn(),
							fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
							"rename or remove the duplicate field",
						)
					}
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
						return nil, galaerr.NewCodedSemanticError(
							galaerr.CodeInterfaceMethodRedeclared,
							msCtx.Identifier().GetStart().GetLine(), msCtx.Identifier().GetStart().GetColumn(),
							fmt.Sprintf("method %q already declared in interface %q", methodName, typeName),
							"rename or remove the duplicate method",
						)
					}
					seenSpecs[methodName] = true
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
						// Reject duplicate field names within a shorthand
						// struct declaration. Fields was cleared above on
						// re-analysis, so this check is scoped to one parse.
						if _, exists := meta.Fields[fieldName]; exists {
							return nil, galaerr.NewCodedSemanticError(
								galaerr.CodeStructFieldRedeclared,
								pctx.Identifier().GetStart().GetLine(), pctx.Identifier().GetStart().GetColumn(),
								fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
								"rename or remove the duplicate field",
							)
						}
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
			if err := a.analyzeSealedType(ctx, pkgName, richAST); err != nil {
				return nil, err
			}
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

	// 1.6 Reject struct field names that collide with another type's name in
	// the same package, but ONLY when both the containing struct and the
	// shadowed type are generic. The IIFE param-type generator produces
	// invalid Go (duplicated type args, e.g. `Mode[T][T]`) when a method
	// receiver's `match` scrutinee is a field whose name shadows a generic
	// type in scope; without generics on either side, the codegen works.
	// Narrowing to "both generic" preserves legitimate non-generic patterns
	// like `struct Route(Handler Handler)`.
	for _, meta := range richAST.Types {
		if meta.Package != pkgName || meta.DefinedIn != filePath {
			continue
		}
		if len(meta.FieldNames) == 0 || len(meta.TypeParams) == 0 {
			continue
		}
		for _, fieldName := range meta.FieldNames {
			otherFullName := fieldName
			if pkgName != "" && pkgName != "main" && pkgName != "test" {
				otherFullName = pkgName + "." + fieldName
			}
			otherMeta, ok := richAST.Types[otherFullName]
			if !ok || otherMeta.Package != pkgName {
				continue
			}
			// A field named after the struct's own type is a separate concern; skip.
			if otherMeta.Name == meta.Name {
				continue
			}
			// Only flag when the shadowed type is itself generic — that's the
			// case where the IIFE param-type doubling kicks in.
			if len(otherMeta.TypeParams) == 0 {
				continue
			}
			pos := meta.FieldPositions[fieldName]
			return nil, galaerr.NewCodedSemanticError(
				galaerr.CodeFieldNameCollidesWithType,
				pos.Line, pos.Column,
				fmt.Sprintf("field %q in generic %q shares its name with generic type %q in package %q", fieldName, meta.Name, otherMeta.Name, pkgName),
				fmt.Sprintf("rename the field (e.g. %q → %q) so it does not shadow the type name", fieldName, "M"),
			)
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
							// Record val/var status so call-site argument transformation
							// can lift bare T values (e.g. string literals) into the
							// Immutable[T] wrapper that `val` parameters end up with in
							// the generated Go signature.
							funcMeta.ParamImmutFlags = append(funcMeta.ParamImmutFlags, paramCtx.VAL() != nil)
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
				// Reject redeclaration of a top-level function within the same
				// package. The sibling-metadata pass may have already registered
				// the function from another file; if it lives in a different
				// file, that's a true redeclaration (mirrors Go's "redeclared
				// in this block"). Skip when the existing entry is empty
				// (placeholder / cache) or refers to this same file
				// (re-analysis of the same source).
				if existing, ok := richAST.Functions[fullFuncName]; ok {
					if existing.DefinedIn != "" && !(existing.DefinedIn != filePath && isSameFile(existing.DefinedIn, absFilePath)) {
						return nil, galaerr.NewCodedSemanticError(
							galaerr.CodeFunctionRedeclared,
							ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
							fmt.Sprintf("function %q in package %q redeclared (first defined in %s)", funcName, pkgName, existing.DefinedIn),
							"remove the duplicate declaration or rename one of the functions",
						)
					}
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
			a.extractPackageVals(sibTree, pkgName, richAST)
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
			a.extractPackageVals(sibTree, pkgName, richAST)
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

	// 2b. Record this file's package-level val/var declarations so cross-file
	// references unwrap their std.Immutable[T] wrapper correctly.
	a.extractPackageVals(sourceFile, pkgName, richAST)

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

	// GALA-E0025: enforce explicit cross-package imports in this file.
	// We've finished collecting metadata for THIS file's own types and
	// functions (extractSiblingFullMetadata earlier did the siblings).
	// Now walk this file's entries and check that every NamedType /
	// GenericType referenced by a signature points to a package that
	// the file itself imported (or std, or this package's own name).
	// If not, that's the auto-import fallback we used to silently
	// take — fail loud so users get a clear "add the import" message.
	if isTopLevel {
		canonFile, _ := filepath.Abs(filePath)
		if real, err := filepath.EvalSymlinks(canonFile); err == nil {
			canonFile = real
		}
		// Build the set of *known GALA package names* that appear as
		// values in richAST.Packages — those are the only packages we
		// validate against. Anything else is either a Go-side import
		// (validated separately by the Go compiler) or a typo we
		// can't usefully diagnose at this layer.
		knownGalaPkgs := make(map[string]bool, len(richAST.Packages))
		for _, name := range richAST.Packages {
			if name != "" {
				knownGalaPkgs[name] = true
			}
		}
		if errs := validateExplicitImports(richAST, canonFile, explicitImportPkgs, knownGalaPkgs); len(errs) > 0 {
			return nil, errs[0]
		}
	}

	logPhase("finalize", phaseStart)
	if profiler.Enabled && isTopLevel {
		fmt.Fprintf(os.Stderr, "  [analyze] %-35s %s\n", "TOTAL", time.Since(analyzeStart).Round(time.Millisecond))
	}

	return richAST, nil
}

// validateExplicitImports walks the metadata for entries defined in
// `canonFile` and reports any NamedType reference whose Package isn't
// in `explicit`. The check covers function signatures (params + return
// types) and struct field types — every place where the analyzer
// previously fell back to "default to current package" qualification
// when a bare name didn't resolve. Mirrors Go's compile-time rule that
// every cross-package symbol needs an explicit import in the file
// using it.
func validateExplicitImports(richAST *transpiler.RichAST, canonFile string, explicit, knownGala map[string]bool) []*galaerr.SemanticError {
	var errs []*galaerr.SemanticError
	// Per-call memo: many TypeMetadata / FunctionMetadata entries share
	// the same DefinedIn (e.g. all 100+ types declared in one .gala
	// file). The original closure called filepath.Abs +
	// filepath.EvalSymlinks once per metadata entry; on the apex
	// transpile of cmd/main.gala in gala_team, richAST.Types +
	// richAST.Functions reach ~1000 entries (full transitive closure),
	// turning this into 1000+ filesystem syscalls. CPU-profile of the
	// gala-build perf test had filepath.EvalSymlinks at 56% of total
	// CPU time, with this closure responsible for 26% on its own.
	// Memoizing by raw input path collapses the cost to one syscall
	// per unique DefinedIn. The fast-path `abs == canonFile` check
	// also lets us skip EvalSymlinks entirely when the paths already
	// match — Bazel sandboxes typically deliver pre-canonicalized
	// paths, so the symlink resolution is dead work in the common case.
	resolvedCache := make(map[string]bool)
	isThisFile := func(p string) bool {
		if p == "" {
			return false
		}
		if cached, ok := resolvedCache[p]; ok {
			return cached
		}
		abs, _ := filepath.Abs(p)
		match := abs == canonFile
		if !match {
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				match = real == canonFile
			}
		}
		resolvedCache[p] = match
		return match
	}
	check := func(t transpiler.Type, pos transpiler.SourcePos, ctxName string) {
		walkTypeForUnresolvedPackages(t, explicit, knownGala, &errs, pos, ctxName)
	}
	for fname, fm := range richAST.Functions {
		if fm == nil || !isThisFile(fm.DefinedIn) {
			continue
		}
		for _, pt := range fm.ParamTypes {
			check(pt, fm.Pos, fname)
		}
		if fm.ReturnType != nil {
			check(fm.ReturnType, fm.Pos, fname)
		}
	}
	for tname, tm := range richAST.Types {
		if tm == nil || !isThisFile(tm.DefinedIn) {
			continue
		}
		for _, ft := range tm.Fields {
			check(ft, tm.Pos, tname)
		}
		// Methods on this type are themselves "function-like" — verify
		// their signatures too.
		for mname, mm := range tm.Methods {
			if mm == nil {
				continue
			}
			for _, pt := range mm.ParamTypes {
				check(pt, mm.Pos, tname+"."+mname)
			}
			if mm.ReturnType != nil {
				check(mm.ReturnType, mm.Pos, tname+"."+mname)
			}
		}
		// Sealed variants — each variant's field types
		for _, sv := range tm.SealedVariants {
			for _, ft := range sv.FieldTypes {
				check(ft, sv.Pos, tname+"."+sv.Name)
			}
		}
	}
	return errs
}

// walkTypeForUnresolvedPackages recursively descends a transpiler.Type
// looking for NamedType nodes whose Package is a known GALA package
// but isn't in `explicit`. Each such reference becomes a coded error
// against CodeUnresolvedCrossPackageSymbol (GALA-E0025): GALA mirrors
// Go's rule that every cross-package symbol needs an explicit import
// in the file using it. Packages not in `knownGala` are treated as
// Go-side (validated by the Go compiler) and skipped.
func walkTypeForUnresolvedPackages(t transpiler.Type, explicit, knownGala map[string]bool, errs *[]*galaerr.SemanticError, pos transpiler.SourcePos, ctxName string) {
	if t == nil {
		return
	}
	switch tt := t.(type) {
	case transpiler.NamedType:
		if tt.Package != "" && !explicit[tt.Package] && knownGala[tt.Package] {
			msg := fmt.Sprintf("undefined: %s (used in %s) — '%s' is not imported in this file",
				tt.Name, ctxName, tt.Package)
			hint := fmt.Sprintf(
				"add an explicit import to this file. For unqualified usage: `import . \"<path-ending-in-%s>\"`. "+
					"For qualified usage: `import \"<path>\"` and call it as `%s.%s`. Sibling files' imports do not propagate.",
				tt.Package, tt.Package, tt.Name)
			*errs = append(*errs, galaerr.NewCodedSemanticError(
				galaerr.CodeUnresolvedCrossPackageSymbol,
				pos.Line, pos.Column, msg, hint))
		}
	case transpiler.GenericType:
		walkTypeForUnresolvedPackages(tt.Base, explicit, knownGala, errs, pos, ctxName)
		for _, p := range tt.Params {
			walkTypeForUnresolvedPackages(p, explicit, knownGala, errs, pos, ctxName)
		}
	case transpiler.ArrayType:
		walkTypeForUnresolvedPackages(tt.Elem, explicit, knownGala, errs, pos, ctxName)
	case transpiler.MapType:
		walkTypeForUnresolvedPackages(tt.Key, explicit, knownGala, errs, pos, ctxName)
		walkTypeForUnresolvedPackages(tt.Elem, explicit, knownGala, errs, pos, ctxName)
	case transpiler.PointerType:
		walkTypeForUnresolvedPackages(tt.Elem, explicit, knownGala, errs, pos, ctxName)
	case transpiler.FuncType:
		for _, pt := range tt.Params {
			walkTypeForUnresolvedPackages(pt, explicit, knownGala, errs, pos, ctxName)
		}
		for _, rt := range tt.Results {
			walkTypeForUnresolvedPackages(rt, explicit, knownGala, errs, pos, ctxName)
		}
	}
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
// Returns a non-nil error if the declaration is rejected (e.g. duplicate
// variant case names within the same sealed type).
func (a *galaAnalyzer) analyzeSealedType(ctx *grammar.SealedTypeDeclarationContext, pkgName string, richAST *transpiler.RichAST) error {
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
		// Reject duplicate sealed-variant case names within a single sealed
		// type declaration. Without this guard the second variant's
		// companion type and Apply/Unapply/isXxx methods would be silently
		// overwritten by the later case, dropping reachable code. Scoped to
		// one parse of one sealed type, so no re-analysis guard is needed.
		for _, existing := range variants {
			if existing.name == variantName {
				return galaerr.NewCodedSemanticError(
					galaerr.CodeSealedVariantCaseRedeclared,
					sc.Identifier().GetStart().GetLine(), sc.Identifier().GetStart().GetColumn(),
					fmt.Sprintf("sealed case %q already declared in sealed type %q", variantName, typeName),
					"rename or remove the duplicate case",
				)
			}
		}
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

	// Preserve methods from any pre-existing placeholder entry. Sibling
	// extraction may have processed a method on this sealed type from another
	// file (e.g. `func (w Widget) AsFixed(...)` in fluent.gala) before this
	// declaration was reached. That earlier pass creates a placeholder
	// TypeMetadata at extractSiblingFullMetadata's method branch (the `else`
	// arm where richAST.Types[fullBaseType] is created with just the method).
	// Without this merge, the assignment below would clobber those methods,
	// breaking return-type resolution at the call site — which in turn breaks
	// auto-unwrap of Immutable[T] fields on chained results.
	if existing, ok := richAST.Types[fullTypeName]; ok {
		for name, m := range existing.Methods {
			if _, dup := parentMeta.Methods[name]; !dup {
				parentMeta.Methods[name] = m
			}
		}
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
	return nil
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
		extractIndices := computeExtractIndices(applyMethod, containerTypeParams)

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

// computeExtractIndices determines which type parameter indices are
// extracted by a companion object. It looks at the Apply method's
// parameters and finds their positions in the container's type
// parameters. Zero-arity extractors (like None) get an empty slice —
// they match without binding values.
func computeExtractIndices(applyMethod *transpiler.MethodMetadata, containerTypeParams []string) []int {
	var indices []int
	for _, paramType := range applyMethod.ParamTypes {
		if transpiler.IsUnusable(paramType) {
			continue
		}
		paramTypeName := normalizeTypeName(paramType.String())
		for idx, containerParam := range containerTypeParams {
			if normalizeTypeName(containerParam) == paramTypeName {
				indices = append(indices, idx)
				break
			}
		}
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
	// type inference for `for k, v := range` over method-returned maps.
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
//
// mergeVisited is the closure-walk dedup set scoped to the enclosing top-level
// Analyze call. Pass nil to allocate one fresh per scan; pass the Analyze-level
// set to share visited bookkeeping across the whole call so sibling imports
// don't re-walk the std/collection_immutable subtree the main file already merged.
func (a *galaAnalyzer) scanImports(sf *grammar.SourceFileContext, richAST *transpiler.RichAST, mergeVisited map[string]bool) {
	if mergeVisited == nil {
		mergeVisited = make(map[string]bool)
	}
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
					a.mergeAnalyzedClosureAt(richAST, path, mergeVisited)
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
						a.storeAnalyzedPkg(path, importedAST)
						a.mergeAnalyzedClosureAt(richAST, path, mergeVisited)
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
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodePackageNotFound,
			0, 0,
			fmt.Sprintf("package not found: %s", relPath),
			"check that the directory exists on a search path; for cross-module imports verify gala.mod has a `require` (and `replace` if local) for the module",
		)
	}

	// In-memory result cache: same dirPath → same RichAST, for the lifetime
	// of this analyzer. Avoids the disk-cache.Get + gob.Decode +
	// rehydrateImports re-walk that the original disk-only path performed
	// every time analyzePackage was called for a package the closure walker
	// had already visited via a different import path. The visit count for
	// a hub package like std on cmd/main.gala can reach the dozens; even
	// at ~50 ms per visit on CI, this dominates the apex transpile.
	if a.pkgResultCache != nil {
		if entry, ok := a.pkgResultCache[dirPath]; ok && entry != nil {
			// entry.pkgAST is already fully merged: both producers
			// (post-fresh-analyze at the bottom of this function and
			// post-disk-rehydrate after a cache.Get) store the AST after
			// its closure has been merged in. Re-walking the closure on
			// every subsequent visit just re-runs idempotent merges and
			// the closure walker — pure overhead, multiplied by the
			// number of times a hub package (std, collection_immutable)
			// is reached via different import paths during one apex
			// transpile. Skipping the rehydrate keeps the in-memory
			// fast-path O(1) per visit instead of O(closure size).
			return entry.pkgAST, nil
		}
	}

	// Check disk cache before doing expensive analysis.
	// The cache key combines the package's own content hash with a hash of its
	// import paths (dependency identity). This ensures the cache invalidates when:
	// 1. Any source file in the package changes (contentHash)
	// 2. The set of imports changes (depsHash identity)
	// 3. Any imported package's content changes (depsHash includes resolved dep hashes)
	contentHash := hashPackageDir(dirPath)
	depsHash := hashImportPaths(dirPath)
	directImports := extractDirectImportPaths(dirPath)
	if contentHash != "" && a.cache != nil {
		cacheStart := time.Now()
		if cached, cachedDirectImports := a.cache.Get(relPath, contentHash, depsHash); cached != nil {
			// Re-merge the package's direct imports into the cached own-only
			// pkgAST. Each import resolves through analyzePackage, which is
			// itself cache-served — so a deep dependency graph is recovered
			// in memory without any of the transitive duplication that
			// previously bloated the on-disk representation.
			a.rehydrateImports(relPath, cached, cachedDirectImports)
			if a.pkgResultCache != nil {
				a.pkgResultCache[dirPath] = &pkgResultCacheEntry{
					pkgAST:        cached,
					directImports: cachedDirectImports,
				}
			}
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

	// Collect candidate file paths so the parses can run in parallel.
	// Only non-test .gala files are eligible; the per-file analysis loop
	// below still runs sequentially because Analyze mutates galaAnalyzer
	// state (currentRichAST, currentDotImportPkgs) that is not safe to
	// share across goroutines. Parse, however, is the dominant per-file
	// cost (~80% of analyzePackage on a freshly-loaded package), and the
	// parsedFileCache it populates is the same one that subsequent
	// SetPackageFiles + Analyze passes hit.
	var candidatePaths []string
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".gala" && !strings.HasSuffix(f.Name(), "_test.gala") {
			candidatePaths = append(candidatePaths, filepath.Join(dirPath, f.Name()))
		}
	}
	parsedTrees := a.parseFilesConcurrent(candidatePaths)

	for i, tree := range parsedTrees {
		filePath := candidatePaths[i]
		if tree == nil {
			continue
		}
		// Mimic the original per-file loop body — Analyze does the heavy
		// recursive work and is intentionally serialized; parsing is the
		// part that benefited from parallelism above.
		{
			res, err := a.Analyze(tree, filePath)
			if err == nil {
				if pkgAST.PackageName == "" {
					pkgAST.PackageName = res.PackageName
				} else if pkgAST.PackageName != res.PackageName {
					// Mirror the explicit --package-files path (line ~308) which
					// already raises CodeDuplicatePackageName for the same
					// condition. The auto-discovery walk had been emitting an
					// uncoded fmt.Errorf; both paths now produce the same code
					// so tools and CI can match on a single identifier.
					return nil, galaerr.NewCodedSemanticError(
						galaerr.CodeDuplicatePackageName,
						0, 0,
						fmt.Sprintf("multiple package names in directory %s: %s and %s", dirPath, pkgAST.PackageName, res.PackageName),
						"use the same package name across all sibling .gala files, or move the file to a different directory",
					)
				}
				// Canonicalize filePath once — isSameFile resolves symlinks and
				// runs os.Stat, which is too expensive to call per type.
				// Same per-call memoization + basename-skip optimization
				// as validateExplicitImports above: most DefinedIn entries
				// are obviously different files (different basename) and
				// don't need a syscall to confirm.
				canonFile, _ := filepath.Abs(filePath)
				if real, err := filepath.EvalSymlinks(canonFile); err == nil {
					canonFile = real
				}
				canonFileBase := filepath.Base(canonFile)
				canonCache := map[string]string{filePath: canonFile}
				sameAsFilePath := func(p string) bool {
					if p == "" {
						return false
					}
					if c, ok := canonCache[p]; ok {
						return c == canonFile
					}
					abs, _ := filepath.Abs(p)
					if abs == canonFile {
						canonCache[p] = abs
						return true
					}
					// Basename mismatch can never be the same file (modulo
					// cross-directory symlinks, vanishingly rare); skip
					// the EvalSymlinks syscall.
					if filepath.Base(abs) != canonFileBase {
						canonCache[p] = abs
						return false
					}
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
	// Extract GoExports unconditionally. Even in mixed GALA+Go packages
	// we need to know the full set of exported Go-level symbols for
	// dot-import collision detection — otherwise facade packages that
	// re-export callables from another Go package via `var X = other.X`
	// (e.g. concurrent re-exporting go_interop's helpers) silently collide
	// at Go compile time instead of producing a clean GALA-level error.
	//
	// In mixed GALA+Go packages we deliberately *exclude* .gen.go files from
	// this scan: those files are auto-generated derivatives of the .gala
	// source and contribute the exact same symbols that already entered
	// pkgAST.Types/Functions through the GALA analyzer above. Re-extracting
	// them here is at best redundant and at worst actively harmful — a stale
	// .gen.go left behind after its .gala counterpart was moved/renamed
	// (e.g. extracted into a subpackage) would otherwise re-introduce a
	// phantom export under the parent package's name and trip the dot-import
	// collision check when a sibling subpackage re-exports the same symbol.
	// Hand-written .go (non-.gen.go) files are still scanned because that's
	// where facade-pattern `var X = other.X` re-exports live.
	//
	// When no .gala source contributed metadata for this package (e.g., the
	// directory only contains .gen.go from a precompiled artifact), allow
	// .gen.go files to participate so cross-module GoExports stay populated.
	// All four maps must be empty: a func-only or alias-only .gala package
	// would otherwise fall through to includeGenerated=true and re-introduce
	// the phantom-export bug PR #237 fixed (B5).
	includeGenerated := len(pkgAST.Types) == 0 &&
		len(pkgAST.Functions) == 0 &&
		len(pkgAST.CompanionObjects) == 0 &&
		len(pkgAST.TypeAliases) == 0
	a.extractGoFileExports(files, dirPath, relPath, pkgAST, includeGenerated)
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
	// Synthesize TypeMetadata entries for Go-defined struct types when no
	// .gala source contributed metadata for this package. This covers
	// pure-Go packages (e.g. a hand-written `bridge.go` exposing a generic
	// struct) so the transformer can lower constructor calls to struct
	// literals or `{}.Apply()` shapes rather than bare function-call form
	// that Go would interpret as a type conversion. In-repo packages with
	// .gala source are unaffected because their TypeMetadata is already
	// populated by the GALA analyzer.
	if includeGenerated {
		synthesizeTypeMetadataFromGo(pkgAST, goInfo)
	}

	// Store in disk cache for future processes. Only the package's OWN
	// metadata (types, functions, methods declared in this package) is
	// persisted — the direct import list is recorded separately so the
	// transitive type closure can be reconstructed at load time without
	// inflating each on-disk entry to N×M of the dependency graph.
	if contentHash != "" && a.cache != nil {
		a.cache.Put(relPath, contentHash, depsHash, pkgAST, directImports)
	}

	// Memoize for in-process reuse so the closure walker doesn't re-do the
	// disk-cache lookup + rehydrate dance the next time this package is
	// reached through a different import path.
	if a.pkgResultCache != nil {
		a.pkgResultCache[dirPath] = &pkgResultCacheEntry{
			pkgAST:        pkgAST,
			directImports: directImports,
		}
	}

	return pkgAST, nil
}

// storeAnalyzedPkg projects `importedAST` to its own-only form and records
// it in analyzedPkgs along with the direct imports needed to recover the
// transitive closure on demand. Returning the projection lets the caller
// substitute it for `importedAST` in subsequent richAST.Merge calls so the
// immediate Merge into the consumer is bounded by the package's own metadata
// rather than its full closure (the closure is reconstructed by walking
// directImports recursively — see mergeAnalyzedClosureAt).
//
// The projection shares Type/Function/Method pointers with `importedAST`
// (it filters keys, not data), so this is cheap and the caller's continued
// use of `importedAST` is safe.
func (a *galaAnalyzer) storeAnalyzedPkg(path string, importedAST *transpiler.RichAST) *transpiler.RichAST {
	if importedAST == nil {
		return nil
	}
	own := projectOwnRichAST(importedAST)
	a.analyzedPkgs[path] = own
	if a.analyzedPkgImports != nil {
		a.analyzedPkgImports[path] = extractDirectGalaImports(importedAST)
	}
	return own
}

// mergeAnalyzedClosureAt walks the in-memory analyzedPkgs entry for `path`
// and recursively merges the own-only projections of all transitively-
// reachable GALA packages into `target`. This reconstructs, on demand, the
// type closure that was previously stored eagerly at every analyzedPkgs
// entry — except the closure now lives only in `target` (the consumer's
// richAST), so the analyzedPkgs cache stays small.
//
// `visited` deduplicates work within a single closure walk. It is allocated
// fresh per call site (one per direct import in the consumer's source file)
// because RichAST.Merge is idempotent under repeated visits — the COW guard
// at transpiler.go:124 short-circuits when the existing entry already has
// every method/field/sealed-variant the merged copy would contribute. An
// over-eager walk costs only map lookups, never data duplication.
//
// Returns the package name of `path` if known (so callers can populate
// target.Packages[path]).
func (a *galaAnalyzer) mergeAnalyzedClosureAt(target *transpiler.RichAST, path string, visited map[string]bool) string {
	if visited == nil || target == nil {
		return ""
	}
	if visited[path] {
		// Already merged in this walk — but caller may still want pkgName.
		if cached := a.analyzedPkgs[path]; cached != nil {
			return cached.PackageName
		}
		return ""
	}
	visited[path] = true
	cached := a.analyzedPkgs[path]
	if cached == nil {
		return ""
	}
	target.Merge(cached)
	for _, imp := range a.analyzedPkgImports[path] {
		if imp == "" || imp == path {
			continue
		}
		impPkg := a.mergeAnalyzedClosureAt(target, imp, visited)
		if impPkg != "" && impPkg != "main" && impPkg != "test" {
			if target.Packages == nil {
				target.Packages = make(map[string]string)
			}
			if _, ok := target.Packages[imp]; !ok {
				target.Packages[imp] = impPkg
			}
		}
	}
	return cached.PackageName
}

// rehydrateImports re-merges the metadata of each direct import into the
// loaded own-only pkgAST. The recursion bottoms out at packages with no
// imports; cycle protection is handled by the analyzer's analyzedPkgs map
// (a placeholder nil entry blocks re-entry while a package is in progress).
//
// pkgPath is the path of the package being rehydrated and is excluded from
// its own import list as a defensive guard against pathological cycles
// where a serialized cache might list itself.
//
// Note: analyzedPkgs entries are themselves own-only projections (see
// projectOwnRichAST), so a single Merge of `analyzedPkgs[imp]` would only
// bring `imp`'s own types in. We use mergeAnalyzedClosureAt to walk the
// transitive closure and pull every reachable package's own metadata into
// pkgAST — this is the same closure walk that callers of analyzedPkgs use
// during a fresh Analyze, applied here to a disk-cache-hit pkgAST.
func (a *galaAnalyzer) rehydrateImports(pkgPath string, pkgAST *transpiler.RichAST, directImports []string) {
	if pkgAST == nil || len(directImports) == 0 {
		return
	}
	if pkgAST.Packages == nil {
		pkgAST.Packages = make(map[string]string)
	}
	visited := make(map[string]bool)
	// The pkgAST itself is own-only (loaded from disk cache); mark it visited
	// so the closure walk doesn't re-merge its own entries.
	visited[pkgPath] = true
	for _, imp := range directImports {
		if imp == "" || imp == pkgPath {
			continue
		}
		// Reuse the in-memory analyzedPkgs cache when possible — this
		// avoids redundant disk reads and keeps cycle detection simple.
		if cached, ok := a.analyzedPkgs[imp]; ok {
			if cached != nil {
				a.mergeAnalyzedClosureAt(pkgAST, imp, visited)
				if cached.PackageName != "" && cached.PackageName != "main" && cached.PackageName != "test" {
					pkgAST.Packages[imp] = cached.PackageName
				}
			}
			continue
		}

		// Determine whether this is a GALA package; non-GALA imports
		// (e.g. Go stdlib) don't have a pkgAST and are handled elsewhere.
		isInternalGala := strings.HasPrefix(imp, "martianoff/gala/")
		isExternalGala := a.resolver != nil && a.resolver.IsGalaPackage(imp)
		if !isInternalGala && !isExternalGala {
			continue
		}
		var relPath string
		if isInternalGala {
			relPath = strings.TrimPrefix(imp, "martianoff/gala/")
		} else {
			relPath = imp
		}

		// Mark as in-progress so a cycle through this import does not loop.
		a.analyzedPkgs[imp] = nil
		importedAST, err := a.analyzePackage(relPath)
		if err != nil || importedAST == nil {
			continue
		}
		a.storeAnalyzedPkg(imp, importedAST)
		a.mergeAnalyzedClosureAt(pkgAST, imp, visited)
		if importedAST.PackageName != "" && importedAST.PackageName != "main" && importedAST.PackageName != "test" {
			pkgAST.Packages[imp] = importedAST.PackageName
		}
	}
}

// synthesizeTypeMetadataFromGo populates pkgAST.Types with TypeMetadata
// entries derived from Go type information (`pkgAST.GoTypeInfo`). It is
// intentionally narrow: a struct type becomes a TypeMetadata so the
// transformer can find its methods (e.g. Apply) and field names. Types
// that already have a TypeMetadata entry from a real .gala source are
// left untouched, so this is a no-op for packages with .gala source.
//
// The synthesized metadata is the minimum required for the call dispatcher's
// gates (zero-arg Apply, generic Apply detection, struct-literal construction)
// to recognize the type. Field information is included so downstream callers
// that build composite literals from positional args still work.
func synthesizeTypeMetadataFromGo(pkgAST *transpiler.RichAST, goInfo *transpiler.GoTypeInfo) {
	if goInfo == nil || len(goInfo.Types) == 0 {
		return
	}
	if pkgAST.Types == nil {
		pkgAST.Types = make(map[string]*transpiler.TypeMetadata)
	}
	for qualName, td := range goInfo.Types {
		if td == nil || td.Kind != "struct" {
			continue
		}
		if _, exists := pkgAST.Types[qualName]; exists {
			continue
		}
		// Split "pkg.Name" into package and simple name. qualName is always
		// dotted ("pkg.Name") for entries originating from extractPackageInfo.
		dot := strings.LastIndex(qualName, ".")
		if dot < 0 {
			continue
		}
		pkgName := qualName[:dot]
		simpleName := qualName[dot+1:]

		methods := make(map[string]*transpiler.MethodMetadata, len(td.Methods))
		for mName, sig := range td.Methods {
			if sig == nil {
				continue
			}
			paramTypes := make([]transpiler.Type, 0, len(sig.Params))
			paramNames := make([]string, 0, len(sig.Params))
			for _, p := range sig.Params {
				paramTypes = append(paramTypes, p.Type)
				paramNames = append(paramNames, p.Name)
			}
			var retType transpiler.Type
			if len(sig.Returns) > 0 {
				retType = sig.Returns[0]
			}
			methods[mName] = &transpiler.MethodMetadata{
				Name:       mName,
				Package:    pkgName,
				ParamTypes: paramTypes,
				ParamNames: paramNames,
				ReturnType: retType,
			}
		}

		// Use the declaration-order slice captured during Go-type extraction
		// so positional composite literals match the field layout. Iterating
		// the Fields map directly is non-deterministic and produces wrong
		// codegen on platforms whose iteration order differs from the
		// struct's declared field order.
		var fieldNames []string
		if len(td.FieldOrder) > 0 {
			fieldNames = append(fieldNames, td.FieldOrder...)
		} else {
			// Fallback for callers that hand-build a GoTypeData without
			// setting FieldOrder. Sorts lexicographically so the result is
			// at least deterministic across runs.
			fieldNames = make([]string, 0, len(td.Fields))
			for fName := range td.Fields {
				fieldNames = append(fieldNames, fName)
			}
			sort.Strings(fieldNames)
		}

		// Derive ImmutFlags from each field type: a field whose Go type is
		// std.Immutable[T] (the wrapper produced by GALA codegen for `val`
		// struct fields) must be flagged immutable so downstream auto-unwrap
		// in comparisons like `t.Field == x` inserts the required `.Get()`.
		// Without this, cross-package access through a Go-only metadata
		// source emits `Immutable[T] == T` and Go rejects the comparison.
		immutFlags := make([]bool, len(fieldNames))
		for i, fName := range fieldNames {
			if fType, ok := td.Fields[fName]; ok && isGoFieldImmutable(fType) {
				immutFlags[i] = true
			}
		}

		pkgAST.Types[qualName] = &transpiler.TypeMetadata{
			Name:       simpleName,
			Package:    pkgName,
			Methods:    methods,
			Fields:     td.Fields,
			FieldNames: fieldNames,
			ImmutFlags: immutFlags,
			TypeParams: td.TypeParams,
		}
	}
}

// isGoFieldImmutable reports whether a field type extracted from a Go
// source is std.Immutable[T] — i.e. the wrapper that GALA codegen emits
// for `val` (immutable) struct fields. Used by synthesizeTypeMetadataFromGo
// to populate ImmutFlags so downstream auto-unwrap fires on cross-package
// access of types whose only metadata source is the generated .gen.go.
func isGoFieldImmutable(typ transpiler.Type) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}
	base := typ.BaseName()
	return base == transpiler.TypeImmutable ||
		strings.HasSuffix(base, "."+transpiler.TypeImmutable)
}

// hasTypeDefinition returns true if the TypeMetadata represents a full type definition
// (struct with fields or sealed type with variants), as opposed to a type entry that
// only has methods added from another file.
func hasTypeDefinition(meta *transpiler.TypeMetadata) bool {
	return len(meta.FieldNames) > 0 || (meta.IsSealed && len(meta.SealedVariants) > 0)
}

// isSameFile checks whether two paths refer to the same file.
// Handles relative/absolute paths and Bazel symlinks on Linux.
//
// Fast paths (no syscalls beyond the cheap filepath.Abs):
//
//   1. absA == absB → same file
//   2. filepath.Base(absA) != filepath.Base(absB) → can never be the
//      same file unless one is a symlink to the other across
//      directories with different basenames (essentially never in
//      practice). Without this guard, every per-Analyze sibling-
//      filter loop pays two filepath.EvalSymlinks calls per sibling
//      just to discover that file_01.gala and file_02.gala are
//      different — on the gala-build CPU profile this was 22% of
//      total CPU time (1.54s of 7.12s).
//
// Slow path (only reached when basenames match but absolute paths
// differ — i.e. potential symlink alias) keeps the original three-
// strategy resolution for correctness on Bazel local_path_override
// on Linux where the same file is reachable via real and sandbox-
// symlinked paths.
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
	if absA == absB {
		return true
	}
	// Basename mismatch → impossible to be the same file in any
	// realistic filesystem layout. Skip the symlink-resolution
	// syscalls entirely. The previous unconditional fall-through to
	// EvalSymlinks burned thousands of syscalls per apex transpile
	// to discover that obviously-different files were obviously
	// different.
	if filepath.Base(absA) != filepath.Base(absB) {
		return false
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
// Covers plain `type Name struct { ... }` as well as alias form `type Name = other.Name`.
var goExportedTypeRe = regexp.MustCompile(`(?m)^type\s+([A-Z]\w*)(\s+|\s*=)`)

// goExportedVarRe matches exported package-level variable declarations in Go files.
// Captures forms like `var GlobalEC = go_interop.GlobalEC`, which act as function-valued
// re-exports. Without this, facade packages that re-export callables via `var` (e.g.
// concurrent re-exporting go_interop helpers) would silently shadow the original
// exporter under dot-import, causing "X redeclared in this block" at Go compile time
// rather than a clean GALA-level collision error.
var goExportedVarRe = regexp.MustCompile(`(?m)^var\s+([A-Z]\w*)\s*(=|\w)`)

// goExportedConstRe matches exported package-level constant declarations.
var goExportedConstRe = regexp.MustCompile(`(?m)^const\s+([A-Z]\w*)\s*(=|\w)`)

// goPkgNameRe matches the package declaration in Go files.
var goPkgNameRe = regexp.MustCompile(`(?m)^package\s+(\w+)`)

// extractGoFileExports scans .go files in a directory for exported symbol names
// and stores them in pkgAST.GoExports (separate from Types/Functions so they
// don't interfere with type resolution). Used for dot-import clash detection.
//
// includeGenerated controls whether auto-generated `.gen.go` files contribute
// to the result. Pass true only when the package is consumed without GALA
// source (cross-module compiled artifacts); when false, .gen.go files are
// skipped because their contents are duplicates of the .gala source already
// reflected in pkgAST.Types/Functions, and a stale .gen.go (left behind
// after its .gala counterpart was moved) would otherwise pollute GoExports
// with phantom symbols that the dot-import collision check would then flag.
func (a *galaAnalyzer) extractGoFileExports(files []os.FileInfo, dirPath, relPath string, pkgAST *transpiler.RichAST, includeGenerated bool) {
	var symbols []string
	seen := make(map[string]bool)

	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".go" || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		if !includeGenerated && strings.HasSuffix(f.Name(), ".gen.go") {
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

		// Extract exported type names (including `type X = ...` aliases).
		for _, m := range goExportedTypeRe.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				symbols = append(symbols, m[1])
			}
		}

		// Extract exported package-level `var` names (facade re-exports such as
		// `var NewSingleThreadEC = go_interop.NewSingleThreadEC`).
		for _, m := range goExportedVarRe.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				symbols = append(symbols, m[1])
			}
		}

		// Extract exported package-level `const` names.
		for _, m := range goExportedConstRe.FindAllStringSubmatch(src, -1) {
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
	// LSP mode skips this entirely: the generated .gen.go files are never
	// read back by the LSP pipeline (analyzePackage produces all the metadata
	// needed for diagnostics directly from the .gala source). Performing the
	// transpile + os.WriteFile here once per import on every DidOpen is the
	// dominant cost of cross-package diagnostics under parallel load on
	// Windows; skipping it brings the test from ~7s back to ~2s under the
	// `--runs_per_test=20 --jobs=20` benchmark.
	if a.skipTranspileToDisk {
		return nil
	}

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

		// Child analyzer shares the parent's caches so its recursive
		// Analyze hits already-populated entries instead of redoing
		// std + every transitive GALA dep from scratch. Cycle safety
		// still holds: the placeholder pattern (`analyzedPkgs[path]
		// = nil` before recursion) breaks cycles regardless of who
		// owns the map. parsedFileCacheMu must be the *same* mutex
		// instance, not a fresh value-typed copy, or goroutines
		// from parent and child would write to the shared map under
		// different locks.
		tempAnalyzer := &galaAnalyzer{
			parser:             a.parser,
			searchPaths:        a.searchPaths,
			analyzedPkgs:       a.analyzedPkgs,
			analyzedPkgImports: a.analyzedPkgImports,
			checkedDirs:        a.checkedDirs,
			siblingTreeCache:   a.siblingTreeCache,
			parsedFileCache:    a.parsedFileCache,
			parsedFileCacheMu:  a.parsedFileCacheMu,
			pkgResultCache:     a.pkgResultCache,
			resolver:           a.resolver,
			cache:              a.cache,
		}

		richAST, err := tempAnalyzer.Analyze(tree, srcPath)
		if err != nil {
			return fmt.Errorf("failed to analyze %s: %w", srcPath, err)
		}

		// Source-mapped stack traces for imported/std GALA packages. Unlike the
		// main Transpile() path, ensureTranspiled writes generated Go directly
		// via Transform+Generate, so we set FilePath (which makes the transformer
		// stamp per-statement / per-declaration line markers for THIS file) and
		// then run the marker->`//line` rewrite ourselves below. A panic inside
		// e.g. std/option.gala then reports option.gala:<n> rather than a
		// generated-Go position. These two steps are atomic: FilePath emits raw
		// `__gala_line_N` markers (undefined identifiers) that ONLY the rewrite
		// turns into valid `//line` directives — skipping it would break the
		// build.
		richAST.FilePath = srcPath

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

		// Rewrite the line markers stamped above into Go `//line` directives
		// (see the FilePath assignment). Must run whenever FilePath was set.
		goCode = transpiler.InsertLineDirectives(goCode, srcPath)

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
			if err := a.analyzeSealedType(ctx, pkgName, richAST); err != nil {
				return err
			}
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
								// Record val/var status (mirrors the same field
								// population in the multifile branch above) so the
								// call-site arg transformer can lift bare T values
								// to Immutable[T] when the param was declared val.
								funcMeta.ParamImmutFlags = append(funcMeta.ParamImmutFlags, paramCtx.VAL() != nil)
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

// extractPackageVals records package-level `val`/`var` declarations from a
// source file into richAST.PackageVals. The transformer pre-registers these so
// that a reference to a package-level `val` in another file of the same package
// is unwrapped from its `std.Immutable[T]` wrapper at the use site — exactly as
// a same-file reference already is. Without this, the cross-file reference emits
// the raw wrapper where a plain `T` is expected and `go build` rejects it.
//
// The element type is recorded when it can be determined cheaply (an explicit
// annotation or a literal initializer); otherwise it is left as NilType. The
// unwrap itself only needs the val/var classification, so an unknown type still
// produces correct code — it merely yields weaker downstream type inference for
// that identifier.
func (a *galaAnalyzer) extractPackageVals(sourceFile *grammar.SourceFileContext, pkgName string, richAST *transpiler.RichAST) {
	for _, topDecl := range sourceFile.AllTopLevelDeclaration() {
		var (
			idList   grammar.IIdentifierListContext
			typeCtx  grammar.ITypeContext
			exprList grammar.IExpressionListContext
			isVal    bool
		)
		switch {
		case topDecl.ValDeclaration() != nil:
			vc := topDecl.ValDeclaration().(*grammar.ValDeclarationContext)
			idList = vc.IdentifierList()
			typeCtx = vc.Type_()
			exprList = vc.ExpressionList()
			isVal = true
		case topDecl.VarDeclaration() != nil:
			vc := topDecl.VarDeclaration().(*grammar.VarDeclarationContext)
			idList = vc.IdentifierList()
			typeCtx = vc.Type_()
			exprList = vc.ExpressionList()
			isVal = false
		default:
			continue
		}
		if idList == nil {
			// Tuple-pattern destructuring (`val (a, b) = ...`) — names still
			// reach the same Immutable lowering, but inferring each element's
			// type here is more than the cross-file unwrap requires, so skip.
			continue
		}

		names := idList.(*grammar.IdentifierListContext).AllIdentifier()
		var exprs []grammar.IExpressionContext
		if exprList != nil {
			exprs = exprList.(*grammar.ExpressionListContext).AllExpression()
		}

		for i, idCtx := range names {
			name := idCtx.GetText()
			var valType transpiler.Type = transpiler.NilType{}
			if typeCtx != nil {
				valType = a.resolveTypeWithParams(typeCtx.GetText(), pkgName, nil)
			} else if len(exprs) == len(names) {
				if lit := inferLiteralType(exprs[i].GetText()); lit != "" {
					valType = transpiler.BasicType{Name: lit}
				}
			}
			if richAST.PackageVals == nil {
				richAST.PackageVals = make(map[string]*transpiler.PackageValMetadata)
			}
			// Prefer a known type: don't let a later (e.g. sibling) Nil entry
			// clobber a precise one already recorded for the same name.
			if existing, ok := richAST.PackageVals[name]; ok &&
				!transpiler.IsUnusable(existing.Type) && transpiler.IsUnusable(valType) {
				continue
			}
			richAST.PackageVals[name] = &transpiler.PackageValMetadata{
				Name:  name,
				Type:  valType,
				IsVal: isVal,
			}
		}
	}
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

// parseFileCached reads, parses, and validates a .gala file's package
// clause, caching the result on the analyzer keyed by canonical absolute
// path. Cache entries are invalidated when the file's mtime or size
// changes between calls. Returns (nil, "", nil) for non-readable or
// non-parseable files so the caller can decide whether that's fatal —
// matching the silent-skip behavior of the inline parsing the
// explicit-package-files branch used to do.
//
// Used by the explicit-package-files branch in Analyze (to amortize
// sibling parsing across BatchAnalyzer calls) and by analyzePackage (so
// when the same project files were just parsed during sibling discovery,
// the import-resolution pass does not redo the work).
func (a *galaAnalyzer) parseFileCached(path string) (*grammar.SourceFileContext, string, error) {
	canonPath, err := filepath.Abs(path)
	if err != nil {
		canonPath = path
	}
	info, err := os.Stat(canonPath)
	if err != nil {
		return nil, "", nil
	}
	if a.parsedFileCache != nil {
		a.parsedFileCacheMu.Lock()
		entry, ok := a.parsedFileCache[canonPath]
		if ok {
			if entry.mtime.Equal(info.ModTime()) && entry.size == info.Size() {
				a.parsedFileCacheMu.Unlock()
				return entry.tree, entry.pkgName, nil
			}
			delete(a.parsedFileCache, canonPath)
		}
		a.parsedFileCacheMu.Unlock()
	}
	content, err := ioutil.ReadFile(canonPath)
	if err != nil {
		return nil, "", nil
	}
	tree, err := a.parser.Parse(string(content))
	if err != nil {
		return nil, "", nil
	}
	otherSF, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, "", nil
	}
	pkgClause, ok := otherSF.PackageClause().(*grammar.PackageClauseContext)
	if !ok || pkgClause.Identifier() == nil {
		return nil, "", nil
	}
	pkgName := pkgClause.Identifier().GetText()
	if a.parsedFileCache != nil {
		a.parsedFileCacheMu.Lock()
		a.parsedFileCache[canonPath] = &parsedFileEntry{
			tree:    otherSF,
			pkgName: pkgName,
			mtime:   info.ModTime(),
			size:    info.Size(),
		}
		a.parsedFileCacheMu.Unlock()
	}
	return otherSF, pkgName, nil
}

// parseFilesConcurrent parses the given paths in parallel, populating
// parsedFileCache for each one. Files already in the cache (and still
// fresh per mtime+size) are returned without re-parsing — same staleness
// rule as parseFileCached. Files that fail to parse, fail to stat, or
// lack a package clause are returned with a nil tree at the matching
// index, mirroring parseFileCached's silent-skip behaviour so callers
// can keep their existing slice-position semantics.
//
// Concurrency: ANTLR's antlr4-go/v4 runtime is thread-safe by default
// (its DFA caches are guarded by sync.Mutex inside the library; see
// mutex.go in the antlr module). Each Parse call also constructs its
// own InputStream / Lexer / Parser instances, so the only shared state
// across goroutines is the global ATN/DFA cache that the runtime
// already protects. parsedFileCache writes go through
// parsedFileCacheMu, taken inside parseFileCached.
//
// Parallelism is capped at min(len(paths), GOMAXPROCS) to avoid
// over-saturating small CI runners (ubuntu-latest = 4 vCPU). For the
// common 5-file package this means 4 parses run concurrently and the
// last one queues — still ~4× faster than the sequential loop on the
// cold path that previously dominated `analyze` time.
func (a *galaAnalyzer) parseFilesConcurrent(paths []string) []*grammar.SourceFileContext {
	out := make([]*grammar.SourceFileContext, len(paths))
	if len(paths) == 0 {
		return out
	}
	if len(paths) == 1 {
		tree, _, _ := a.parseFileCached(paths[0])
		out[0] = tree
		return out
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > len(paths) {
		workers = len(paths)
	}
	if workers < 1 {
		workers = 1
	}
	idxCh := make(chan int, len(paths))
	for i := range paths {
		idxCh <- i
	}
	close(idxCh)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idxCh {
				tree, _, _ := a.parseFileCached(paths[i])
				out[i] = tree
			}
		}()
	}
	wg.Wait()
	return out
}

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

