package gooracle

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// TranspileFunc turns one GALA source file into Go. filePath is the file's
// real location, so the analyzer can load the package's sibling files.
type TranspileFunc func(src, filePath string) (string, error)

// ImporterConfig configures an Importer.
type ImporterConfig struct {
	// Go resolves Go packages (the standard library). Nil disables Go
	// imports: every file importing one is skipped, with that as the reason.
	Go types.Importer
	// ModulePath and Root map GALA import paths to directories: an import of
	// ModulePath+"/x/y" is the package in Root/x/y. Every GALA package is
	// resolved this way, the standard library included.
	ModulePath string
	Root       string
	// Transpile turns the package's .gala files into Go.
	Transpile TranspileFunc
}

// Importer is a types.Importer for generated Go. Go packages go to the
// configured Go importer. GALA packages are transpiled from source, joined
// with any hand-written .go files beside them, and type-checked; the result
// is cached for the life of the Importer.
type Importer struct {
	cfg  ImporterConfig
	fset *token.FileSet

	mu    sync.Mutex
	cache map[string]importResult
}

type importResult struct {
	pkg *types.Package
	err error
}

var _ types.Importer = (*Importer)(nil)

// NewImporter returns an Importer for cfg.
func NewImporter(cfg ImporterConfig) *Importer {
	return &Importer{cfg: cfg, fset: token.NewFileSet(), cache: map[string]importResult{}}
}

// UnresolvedImportError means an import could not be found at all: a Go
// package outside the standard library, a GALA package from a module this
// importer does not know, or no Go SDK. A file that needs one cannot be
// type-checked here, which is a skip, not a failure.
type UnresolvedImportError struct {
	Path   string
	Reason string
}

func (e *UnresolvedImportError) Error() string {
	return fmt.Sprintf("cannot resolve import %q: %s", e.Path, e.Reason)
}

// PackageError means a GALA package was found but its generated Go could not
// be produced or does not type-check. That is a real failure, reported
// against the package.
type PackageError struct {
	Path string
	Err  error
}

func (e *PackageError) Error() string {
	return fmt.Sprintf("GALA package %q: %v", e.Path, e.Err)
}

func (e *PackageError) Unwrap() error { return e.Err }

// Import implements types.Importer.
func (i *Importer) Import(path string) (*types.Package, error) {
	i.mu.Lock()
	if r, ok := i.cache[path]; ok {
		i.mu.Unlock()
		return r.pkg, r.err
	}
	i.mu.Unlock()

	pkg, err := i.load(path)

	i.mu.Lock()
	i.cache[path] = importResult{pkg, err}
	i.mu.Unlock()
	return pkg, err
}

func (i *Importer) load(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if i.cfg.ModulePath != "" && strings.HasPrefix(path, i.cfg.ModulePath+"/") {
		return i.loadGala(path, filepath.Join(i.cfg.Root, filepath.FromSlash(strings.TrimPrefix(path, i.cfg.ModulePath+"/"))))
	}
	if i.cfg.Go == nil {
		return nil, &UnresolvedImportError{path, "no Go SDK available to the oracle"}
	}
	if !isStdlibPath(path) {
		return nil, &UnresolvedImportError{path, "not in the Go standard library"}
	}
	pkg, err := i.cfg.Go.Import(path)
	if err != nil {
		return nil, &UnresolvedImportError{path, err.Error()}
	}
	return pkg, nil
}

// isStdlibPath reports whether path looks like a standard-library import: no
// dot in its first element.
func isStdlibPath(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func (i *Importer) loadGala(path, dir string) (*types.Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, &UnresolvedImportError{path, "no package directory at " + dir}
	}

	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_test.gala") {
			continue
		}
		full := filepath.Join(dir, name)
		var src string
		switch {
		case strings.HasSuffix(name, ".gala"):
			if i.cfg.Transpile == nil {
				return nil, &UnresolvedImportError{path, "no transpiler configured"}
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				return nil, &PackageError{path, err}
			}
			src, err = i.cfg.Transpile(string(raw), full)
			if err != nil {
				return nil, &PackageError{path, fmt.Errorf("transpiling %s: %w", name, err)}
			}
			full = strings.TrimSuffix(full, ".gala") + ".gen.go"
		case strings.HasSuffix(name, ".go"):
			// A generated file left behind by an earlier transpile of this
			// directory duplicates the .gala it came from.
			if strings.HasSuffix(name, ".gen.go") {
				continue
			}
			if ok, _ := build.Default.MatchFile(dir, name); !ok {
				continue
			}
			raw, err := os.ReadFile(full)
			if err != nil {
				return nil, &PackageError{path, err}
			}
			src = string(raw)
		default:
			continue
		}
		f, err := parser.ParseFile(i.fset, full, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, &PackageError{path, err}
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, &UnresolvedImportError{path, "no Go or GALA sources in " + dir}
	}

	var errs []string
	conf := types.Config{
		Importer: i,
		Error: func(err error) {
			if te, ok := err.(types.Error); ok && te.Soft {
				return
			}
			errs = append(errs, err.Error())
		},
	}
	pkg, _ := conf.Check(path, i.fset, files, nil)
	if len(errs) > 0 {
		if len(errs) > 5 {
			errs = append(errs[:5], fmt.Sprintf("... and %d more", len(errs)-5))
		}
		return nil, &PackageError{path, errors.New(strings.Join(errs, "\n  "))}
	}
	return pkg, nil
}

// TypeResult is the outcome of type-checking one file.
type TypeResult struct {
	// Skipped is non-empty when the file could not be checked, and says why.
	Skipped string
	// Errors are the hard type errors. Soft errors — unused variables and
	// imports — are counted in Soft instead: unit-test fixtures routinely
	// declare a value only to look at how it lowers.
	Errors []string
	Soft   int
}

// TypeCheck type-checks src as a single-file package. Imports are resolved
// first; if any cannot be, the file is skipped with that as the reason. A
// GALA import that resolves but does not type-check is an error.
func (i *Importer) TypeCheck(filename, src string) TypeResult {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return TypeResult{Errors: []string{"does not parse: " + err.Error()}}
	}

	paths := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return TypeResult{Errors: []string{"bad import path " + spec.Path.Value}}
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var pkgErrs []string
	for _, p := range paths {
		if _, err := i.Import(p); err != nil {
			var ue *UnresolvedImportError
			if errors.As(err, &ue) {
				return TypeResult{Skipped: ue.Error()}
			}
			pkgErrs = append(pkgErrs, err.Error())
		}
	}
	if len(pkgErrs) > 0 {
		return TypeResult{Errors: pkgErrs}
	}

	var res TypeResult
	conf := types.Config{
		Importer: i,
		Error: func(err error) {
			if te, ok := err.(types.Error); ok && te.Soft {
				res.Soft++
				return
			}
			res.Errors = append(res.Errors, err.Error())
		},
	}
	conf.Check(file.Name.Name, fset, []*ast.File{file}, nil)
	return res
}
