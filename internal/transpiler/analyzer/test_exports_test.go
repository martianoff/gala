package analyzer

import (
	"sync"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/module"
	"martianoff/gala/internal/transpiler/registry"
)

// GetBaseMetadata loads standard library metadata. Used by tests to
// inspect what the analyzer pulls in for `std` (e.g. companion-object
// discovery). Production code never needs this — std is loaded
// implicitly via the import scan in Analyze().
//
// File suffix `_test.go` keeps it out of the production binary while
// still exporting it to callers in `package analyzer_test`.
func GetBaseMetadata(p transpiler.GalaParser, searchPaths []string) *transpiler.RichAST {
	a := &galaAnalyzer{
		parser:             p,
		searchPaths:        searchPaths,
		analyzedPkgs:       make(map[string]*transpiler.RichAST),
		analyzedPkgImports: make(map[string][]string),
		checkedDirs:        make(map[string]bool),
		siblingTreeCache:   make(map[string]*siblingCacheEntry),
		parsedFileCache:    make(map[string]*parsedFileEntry),
		parsedFileCacheMu:  &sync.Mutex{},
		pkgResultCache:     make(map[string]*pkgResultCacheEntry),
		resolver:           module.NewResolver(searchPaths),
	}

	stdAST, err := a.analyzePackage(registry.StdPackageName)
	if err != nil {
		return &transpiler.RichAST{
			Types:            make(map[string]*transpiler.TypeMetadata),
			Functions:        make(map[string]*transpiler.FunctionMetadata),
			Packages:         make(map[string]string),
			CompanionObjects: make(map[string]*transpiler.CompanionObjectMetadata),
		}
	}
	return stdAST
}

// NewGalaAnalyzerWithBase pre-seeds an analyzer's metadata. Test-only:
// the `baseMetadata` field is documented as deprecated/legacy and
// nothing in production calls this constructor (verified
// `grep -rn NewGalaAnalyzerWithBase --include='*.go'` outside
// `_test.go` files returns zero hits at the time of this commit).
func NewGalaAnalyzerWithBase(base *transpiler.RichAST, p transpiler.GalaParser, searchPaths []string, projectRoot ...string) transpiler.Analyzer {
	root := ""
	if len(projectRoot) > 0 {
		root = projectRoot[0]
	}
	return &galaAnalyzer{
		baseMetadata:       base,
		parser:             p,
		searchPaths:        searchPaths,
		analyzedPkgs:       make(map[string]*transpiler.RichAST),
		analyzedPkgImports: make(map[string][]string),
		checkedDirs:        make(map[string]bool),
		siblingTreeCache:   make(map[string]*siblingCacheEntry),
		parsedFileCache:    make(map[string]*parsedFileEntry),
		parsedFileCacheMu:  &sync.Mutex{},
		pkgResultCache:     make(map[string]*pkgResultCacheEntry),
		resolver:           module.NewResolver(searchPaths),
		cache:              newAnalysisCache(resolveCacheRoot(root)),
	}
}
