package analyzer

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"sync"

	"martianoff/gala/internal/transpiler/genheader"
)

// moduleDirs records how a type check resolved the imports it read, its own
// and those of the packages it loaded from source: for each import path, the
// source directory a table of Go module sources (see galaAnalyzer.goSrcDirs)
// gave it, or "" when the table gave none and the shared importer served it.
// A result built under one table is valid under another that resolves each
// of those paths the same way, so caches keyed by directory serve every build
// whose table agrees on what they read (a Bazel worker gets a table per target).
type moduleDirs map[string]string

// holdIn reports whether goSrcDirs resolves every recorded import as it was.
func (d moduleDirs) holdIn(goSrcDirs map[string]string) bool {
	for path, dir := range d {
		if got, _ := ResolveGoSrcDir(goSrcDirs, path); got != dir {
			return false
		}
	}
	return true
}

// moduleSourceImporter is the importer the hand-written .go files of a package
// are type-checked with: a package of a module goSrcDirs names is read from
// its source directory, any other through the shared Go importer. It records
// what it resolved in used.
//
// The shared importer reads the standard library, but not a versioned module
// from the module cache or Bazel's external tree. Without the source
// directories a third-party package that imports another module (cobra
// imports pflag) is type-checked with that import unresolved: every type of
// its API naming the other module (Command.Flags() *pflag.FlagSet) is invalid,
// so a call through it has no Go signature and a (T, error) result is not
// lowered to Try.
type moduleSourceImporter struct {
	base      types.Importer // nil without a Go SDK
	goSrcDirs map[string]string
	used      moduleDirs
}

var _ types.Importer = (*moduleSourceImporter)(nil)

func newModuleSourceImporter(goSrcDirs map[string]string) *moduleSourceImporter {
	return &moduleSourceImporter{base: getGoImporter(), goSrcDirs: goSrcDirs, used: moduleDirs{}}
}

func (m *moduleSourceImporter) Import(path string) (*types.Package, error) {
	dir, fromSource := ResolveGoSrcDir(m.goSrcDirs, path)
	m.used[path] = dir
	if fromSource {
		pkg, used, err := moduleSourcePackages.load(dir, path, m.goSrcDirs)
		maps.Copy(m.used, used)
		return pkg, err
	}
	if m.base == nil {
		return nil, fmt.Errorf("cannot find package %q: no Go SDK", path)
	}
	return m.base.Import(path)
}

// moduleSourcePackages memoizes the packages moduleSourceImporter type-checks
// from source, for the lifetime of the process, like goFilesCache: per source
// directory and import path, each variant valid under the tables that resolve
// its imports as it did (see moduleDirs). A package's imports are loaded
// through the same cache, so a module's dependency graph is checked once,
// each package on first use.
var moduleSourcePackages = &sourcePackageCache{entries: map[string]*sourcePackages{}}

type sourcePackageCache struct {
	mu      sync.Mutex
	entries map[string]*sourcePackages
}

// sourcePackages are the checked variants of one package. Its mutex is held
// while a variant is checked, so a package is not checked twice at once; Go
// forbids import cycles, so the nested loads of its imports never wait on it.
type sourcePackages struct {
	mu       sync.Mutex
	variants []sourcePackage
}

type sourcePackage struct {
	pkg  *types.Package
	used moduleDirs
	err  error
}

func (c *sourcePackageCache) load(dir, path string, goSrcDirs map[string]string) (*types.Package, moduleDirs, error) {
	key := dir + "\x00" + path
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		e = &sourcePackages{}
		c.entries[key] = e
	}
	c.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	for _, v := range e.variants {
		if v.used.holdIn(goSrcDirs) {
			return v.pkg, v.used, v.err
		}
	}
	imp := newModuleSourceImporter(goSrcDirs)
	pkg, err := checkSourcePackage(dir, path, imp)
	e.variants = append(e.variants, sourcePackage{pkg: pkg, used: imp.used, err: err})
	return pkg, imp.used, err
}

// checkSourcePackage type-checks the Go package at path from the files of
// dir the go command would build on this host, the selection the package is
// compiled with when imported (analyzeGoFiles, which reads a package GALA code
// calls into, keeps the files of every platform instead). Type errors are
// ignored, as for analyzeGoFiles: a partly checked package still types its
// API. Files the GALA transpiler wrote are left out, as analyzeGoFiles leaves
// them out: a GALA package's types come from its .gala source, so one that has
// nothing else is not loaded here (under Bazel, --go-src names GALA libraries
// too).
func checkSourcePackage(dir, path string, imp types.Importer) (*types.Package, error) {
	bp, _ := build.Default.ImportDir(dir, 0)
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range slices.Concat(bp.GoFiles, bp.CgoFiles) {
		full := filepath.Join(dir, name)
		if genheader.StaleFile(full) {
			continue
		}
		if f, err := parser.ParseFile(fset, full, nil, parser.ParseComments|parser.SkipObjectResolution); err == nil && !genheader.Written(f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no hand-written Go files for package %q in %s", path, dir)
	}
	conf := types.Config{
		Importer:    imp,
		FakeImportC: true,
		Error:       func(error) {},
	}
	pkg, _ := conf.Check(path, fset, files, nil)
	if pkg == nil {
		return nil, fmt.Errorf("cannot type-check package %q in %s", path, dir)
	}
	return pkg, nil
}
