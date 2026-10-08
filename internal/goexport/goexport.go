// Package goexport writes GALA's standard library as one plain Go module that
// a Go program can depend on without the GALA toolchain.
//
// GALA code imports the standard library as martianoff/gala/..., a path the
// go command cannot download because its first element has no dot. An export
// rewrites those imports to a fetchable module path (for example
// go.gala.fyi/stdlib) and writes every package under one go.mod, so a Go
// consumer needs no replace directive. GALA builds never see the exported
// path.
package goexport

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"martianoff/gala/internal/stdlib"
)

// Options configures an export.
type Options struct {
	// ModulePath is the Go module path the export is published under.
	ModulePath string
	// GalaVersion and Commit identify the compiler that produced the export.
	// They are written to the VERSION file.
	GalaVersion string
	Commit      string
}

// SourceModulePath is the module path GALA code imports the standard library
// from, derived from the embedded package table.
func SourceModulePath() string {
	return strings.TrimSuffix(stdlib.PackageImportPaths["std"], "/std")
}

// File is one file of an export, with its slash-separated path relative to
// the module root.
type File struct {
	Path    string
	Content []byte
}

// Stdlib returns the files of the exported standard library module, sorted by
// path: a root go.mod, a VERSION file, and each embedded package's Go and GALA
// sources with every standard library import rewritten to opts.ModulePath.
func Stdlib(opts Options) ([]File, error) {
	if err := checkModulePath(opts.ModulePath); err != nil {
		return nil, err
	}
	from := SourceModulePath()
	if opts.ModulePath == from {
		return nil, fmt.Errorf("module path %q is the path GALA imports the stdlib from; export it under a path the go command can download", from)
	}

	files := []File{
		{Path: "go.mod", Content: []byte("module " + opts.ModulePath + "\n\ngo " + stdlib.GoVersion + "\n")},
		{Path: "VERSION", Content: []byte(fmt.Sprintf("gala %s\ncommit %s\n", opts.GalaVersion, opts.Commit))},
	}
	for pkg, sources := range stdlib.EmbeddedPackages {
		for name, content := range sources {
			p := path.Join(pkg, name)
			data := []byte(content)
			if strings.HasSuffix(name, ".go") {
				var err error
				if data, err = rewriteImports(p, data, from, opts.ModulePath); err != nil {
					return nil, err
				}
			}
			files = append(files, File{Path: p, Content: data})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// rewriteImports replaces the quoted import prefix `"from/` with `"to/`,
// which covers import specs and import examples in comments, then checks
// that the file still parses and mentions from nowhere else.
func rewriteImports(name string, src []byte, from, to string) ([]byte, error) {
	out := bytes.ReplaceAll(src, []byte(`"`+from+`/`), []byte(`"`+to+`/`))
	f, err := parser.ParseFile(token.NewFileSet(), name, out, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parsing %s after rewriting imports: %w", name, err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == from || strings.HasPrefix(p, from+"/") {
			return nil, fmt.Errorf("%s: import %q was not rewritten", name, p)
		}
	}
	if bytes.Contains(out, []byte(from+"/")) {
		return nil, fmt.Errorf("%s still mentions %s/ outside an import; the export cannot rewrite it", name, from)
	}
	return out, nil
}

// checkModulePath accepts a module path the go command can download: its
// first element must look like a domain name.
func checkModulePath(p string) error {
	if p == "" {
		return errors.New("module path is required")
	}
	first, _, _ := strings.Cut(p, "/")
	if !strings.Contains(first, ".") || strings.HasPrefix(first, ".") || strings.HasSuffix(first, ".") {
		return fmt.Errorf("module path %q: the go command only downloads paths whose first element is a domain name", p)
	}
	if strings.HasSuffix(p, "/") || strings.Contains(p, "//") {
		return fmt.Errorf("module path %q is malformed", p)
	}
	return nil
}

// WriteDir writes files under dir. dir must not exist or must be empty, and
// must not lie inside a Bazel workspace, where Gazelle would pick up the
// rewritten imports.
func WriteDir(dir string, files []File) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if ws, ok := enclosingWorkspace(abs); ok {
		return fmt.Errorf("refusing to export into %s: it is inside the Bazel workspace %s; choose a directory outside it", abs, ws)
	}
	if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 {
		return fmt.Errorf("refusing to export into %s: the directory is not empty", abs)
	}
	for _, f := range files {
		target := filepath.Join(abs, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, f.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// enclosingWorkspace reports the nearest ancestor of dir (or dir itself) that
// is a Bazel workspace root.
func enclosingWorkspace(dir string) (string, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		for _, marker := range []string{"MODULE.bazel", "WORKSPACE", "WORKSPACE.bazel", "REPO.bazel"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d, true
			}
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}
