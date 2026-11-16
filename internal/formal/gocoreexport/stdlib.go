package gocoreexport

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// RunnableStdlib lists the files of Go's standard library that GoCore runs as
// Go's own source, by import path: small files that import nothing, so the
// program runs the very code `go run` links, not a model of it. It is reviewed
// like GoCore's library models: a file is added here only with the guards of
// checkRunnable and checkSiblings passing.
var RunnableStdlib = map[string][]string{
	"errors":       {"errors.go"},
	"unicode/utf8": {"utf8.go"},
}

// AddStdlib returns pkgs with the packages of RunnableStdlib the program
// imports (not with `.` or `_`), their listed files read from goroot, marked
// partial: a name the program uses that those files do not declare is one
// GoCore reports as outside what it runs. The error is for a file that cannot
// be read or does not parse, or one the guards refuse. An empty goroot adds
// nothing.
func AddStdlib(goroot string, pkgs []PackageSource) ([]PackageSource, error) {
	if goroot == "" {
		return pkgs, nil
	}
	imported := map[string]bool{}
	for _, p := range pkgs {
		for _, src := range p.Files {
			f, err := parser.ParseFile(token.NewFileSet(), src.Name, src.Text, parser.ImportsOnly)
			if err != nil {
				return nil, err
			}
			for _, spec := range f.Imports {
				ip, err := strconv.Unquote(spec.Path.Value)
				if err != nil || RunnableStdlib[ip] == nil {
					continue
				}
				if spec.Name != nil && (spec.Name.Name == "." || spec.Name.Name == "_") {
					continue
				}
				imported[ip] = true
			}
		}
	}
	paths := make([]string, 0, len(imported))
	for ip := range imported {
		paths = append(paths, ip)
	}
	slices.Sort(paths)
	for _, ip := range paths {
		ps, err := readRunnable(goroot, ip)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, ps)
	}
	return pkgs, nil
}

// readRunnable reads a runnable package's listed files and checks them, and
// the package's other files, with the guards.
func readRunnable(goroot, ip string) (PackageSource, error) {
	dir := filepath.Join(goroot, "src", filepath.FromSlash(ip))
	ps := PackageSource{Path: ip, Partial: true}
	var listed []*ast.File
	for _, name := range RunnableStdlib[ip] {
		text, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ps, err
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, text, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			return ps, err
		}
		if err := checkRunnable(ip+"/"+name, f); err != nil {
			return ps, err
		}
		listed = append(listed, f)
		ps.Files = append(ps.Files, Source{Name: name, Text: string(text)})
	}
	return ps, checkSiblings(ip, dir, listed)
}

// checkRunnable refuses a listed file that GoCore could run differently from
// the program `go run` builds: one with a build constraint (it might not be
// the file the toolchain compiles), a `//go:linkname`, a function without a
// body (assembly), or any import (`unsafe` included). A Go release that
// changes a listed file so fails the export loudly instead of slipping such a
// construct in.
func checkRunnable(where string, f *ast.File) error {
	for _, g := range f.Comments {
		for _, c := range g.List {
			switch {
			case strings.HasPrefix(c.Text, "//go:build"), strings.HasPrefix(c.Text, "// +build"):
				return fmt.Errorf("%s: a build constraint (%s)", where, c.Text)
			case strings.HasPrefix(c.Text, "//go:linkname"):
				return fmt.Errorf("%s: %s", where, c.Text)
			}
		}
	}
	if len(f.Imports) > 0 {
		return fmt.Errorf("%s: imports %s", where, f.Imports[0].Path.Value)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body == nil {
			return fmt.Errorf("%s: %s has no body", where, fd.Name.Name)
		}
	}
	return nil
}

// checkSiblings refuses a runnable package whose other files, which GoCore
// does not run but `go run` links, could change what the listed files do: by
// declaring a method on a type a listed file declares (it would be in the
// type's method set), or by naming a package-level variable a listed file
// declares (code there could set it). What else those files do while they
// initialize, GoCore leaves out as for any quiet package: every runnable
// package is in quietInitPackages (TestRunnableStdlibIsQuiet).
func checkSiblings(ip, dir string, listed []*ast.File) error {
	types, vars := map[string]bool{}, map[string]bool{}
	for _, f := range listed {
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok {
				for _, s := range gd.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						types[s.Name.Name] = true
					case *ast.ValueSpec:
						if gd.Tok == token.VAR {
							for _, n := range s.Names {
								vars[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	for _, path := range names {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") || slices.Contains(RunnableStdlib[ip], name) {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		var bad error
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Recv != nil && len(n.Recv.List) > 0 {
					t := n.Recv.List[0].Type
					if s, ok := t.(*ast.StarExpr); ok {
						t = s.X
					}
					if id, ok := t.(*ast.Ident); ok && types[id.Name] && bad == nil {
						bad = fmt.Errorf("%s/%s: a method on %s, declared in a runnable file", ip, name, id.Name)
					}
				}
			case *ast.Ident:
				if vars[n.Name] && bad == nil {
					bad = fmt.Errorf("%s/%s: names %s, a variable of a runnable file", ip, name, n.Name)
				}
			}
			return true
		})
		if bad != nil {
			return bad
		}
	}
	return nil
}
