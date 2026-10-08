// Package goexport writes GALA-generated Go as a plain Go module that a Go
// program can depend on without the GALA toolchain.
//
// GALA code imports the standard library as martianoff/gala/..., a path the
// go command cannot download because its first element has no dot. An export
// rewrites imports through a table of module paths (for example
// martianoff/gala -> go.gala.fyi/stdlib) and writes every package under one
// go.mod, so a Go consumer needs no replace directive. GALA builds never see
// the exported paths.
package goexport

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/module"

	"martianoff/gala/internal/stdlib"
)

// File is one file of an export, with its slash-separated path relative to
// the module root.
type File struct {
	Path    string
	Content []byte
}

// Module describes one exported Go module.
type Module struct {
	// Path is the Go module path the export is published under.
	Path string
	// GoVersion is the go directive of the exported go.mod.
	GoVersion string
	// Packages maps a package directory (relative to the module root) to its
	// files by name. Go files have their imports rewritten; other files are
	// copied as they are.
	Packages map[string]map[string]string
	// Remap maps a source module path to its exported module path. An import
	// of a remapped module, or of a package under it, is rewritten.
	Remap map[string]string
	// Version is written to the module's VERSION file.
	Version string
}

// Export returns the files of m, sorted by path: a root go.mod, a VERSION
// file and every package's files.
//
// The exported go.mod has no requirements, so every import must resolve to
// the Go standard library or, after remapping, to one of m.Packages; any
// other import is an error rather than a module that cannot build.
func Export(m Module) ([]File, error) {
	if err := module.CheckPath(m.Path); err != nil {
		return nil, err
	}
	for from, to := range m.Remap {
		if from == to {
			return nil, fmt.Errorf("module %s is remapped to itself", from)
		}
		if err := module.CheckPath(to); err != nil {
			return nil, err
		}
	}

	files := []File{
		{Path: "go.mod", Content: []byte("module " + m.Path + "\n\ngo " + m.GoVersion + "\n")},
		{Path: "VERSION", Content: []byte(m.Version)},
	}
	for pkg, sources := range m.Packages {
		for name, content := range sources {
			p := path.Join(pkg, name)
			data := []byte(content)
			if strings.HasSuffix(name, ".go") {
				var err error
				if data, err = rewriteImports(p, data, m); err != nil {
					return nil, err
				}
			}
			files = append(files, File{Path: p, Content: data})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// rewriteImports rewrites the remapped import paths of a Go file in place,
// leaving every other byte (comments, string literals, //line directives)
// untouched, and rejects imports the exported module cannot satisfy.
func rewriteImports(name string, src []byte, m Module) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", name, err)
	}
	var out []byte
	last := 0
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		newPath := remapImport(p, m.Remap)
		if !providedBy(newPath, m) && !isGoStdlib(newPath, m.Remap) {
			return nil, fmt.Errorf("%s imports %q, which the exported module %s does not provide", name, p, m.Path)
		}
		if newPath == p {
			continue
		}
		start, end := fset.Position(imp.Path.Pos()).Offset, fset.Position(imp.Path.End()).Offset
		out = append(append(out, src[last:start]...), strconv.Quote(newPath)...)
		last = end
	}
	if out == nil {
		return src, nil
	}
	return append(out, src[last:]...), nil
}

// remapImport returns p with its module prefix replaced when it belongs to a
// remapped module. The longest matching source module wins, so overlapping
// entries rewrite the same way on every run.
func remapImport(p string, remap map[string]string) string {
	best := ""
	for from := range remap {
		if (p == from || strings.HasPrefix(p, from+"/")) && len(from) > len(best) {
			best = from
		}
	}
	if best == "" {
		return p
	}
	return remap[best] + strings.TrimPrefix(p, best)
}

// providedBy reports whether import path p names one of m's packages.
func providedBy(p string, m Module) bool {
	if p != m.Path && !strings.HasPrefix(p, m.Path+"/") {
		return false
	}
	_, ok := m.Packages[strings.TrimPrefix(strings.TrimPrefix(p, m.Path), "/")]
	return ok
}

// isGoStdlib reports whether p can be a Go standard library import: its first
// element has no dot, and it is not under a remapped source module's first
// element (martianoff/... is never Go's, so an unremapped one is an error).
func isGoStdlib(p string, remap map[string]string) bool {
	first, _, _ := strings.Cut(p, "/")
	if strings.Contains(first, ".") {
		return false
	}
	for from := range remap {
		if fromFirst, _, _ := strings.Cut(from, "/"); fromFirst == first {
			return false
		}
	}
	return true
}

// StdlibOptions configures a standard library export.
type StdlibOptions struct {
	// ModulePath is the Go module path the standard library is published under.
	ModulePath string
	// GalaVersion and Commit identify the compiler that produced the export.
	GalaVersion string
	Commit      string
}

// Stdlib exports the standard library embedded in this binary.
func Stdlib(opts StdlibOptions) ([]File, error) {
	from := strings.TrimSuffix(stdlib.PackageImportPaths["std"], "/std")
	return Export(Module{
		Path:      opts.ModulePath,
		GoVersion: stdlib.GoVersion,
		Packages:  stdlib.EmbeddedPackages,
		Remap:     map[string]string{from: opts.ModulePath},
		Version:   fmt.Sprintf("gala %s\ncommit %s\n", opts.GalaVersion, opts.Commit),
	})
}

// WriteDir writes files under dir, which must not exist or must be empty.
func WriteDir(dir string, files []File) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot export into %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("refusing to export into %s: the directory is not empty", dir)
	}
	for _, f := range files {
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, f.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
