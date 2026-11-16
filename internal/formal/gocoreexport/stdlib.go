package gocoreexport

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// RunnableStdlib lists the files of Go's standard library that GoCore runs as
// Go's own source, by import path: small files that import nothing, so the
// program runs the very code `go run` links, not a model of it.
var RunnableStdlib = map[string][]string{
	"errors":       {"errors.go"},
	"unicode/utf8": {"utf8.go"},
}

// AddStdlib returns pkgs with the packages of RunnableStdlib the program
// imports, read from goroot. A package is added only when every name the
// program selects from it (pkg.Name) is declared in its files, and no file
// imports it with `.` or `_`; otherwise it is left out, and GoCore reports
// the call it cannot run as unsupported. The error is for a file that cannot
// be read or does not parse.
func AddStdlib(goroot string, pkgs []PackageSource) ([]PackageSource, error) {
	used := map[string][]string{} // import path -> names selected
	unusable := map[string]bool{}
	for _, p := range pkgs {
		for _, src := range p.Files {
			f, err := parser.ParseFile(token.NewFileSet(), src.Name, src.Text, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			for _, spec := range f.Imports {
				ip, err := strconv.Unquote(spec.Path.Value)
				if err != nil || RunnableStdlib[ip] == nil {
					continue
				}
				name := path.Base(ip)
				if spec.Name != nil {
					name = spec.Name.Name
				}
				if name == "." || name == "_" {
					unusable[ip] = true
					continue
				}
				if _, seen := used[ip]; !seen {
					used[ip] = nil
				}
				ast.Inspect(f, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
							used[ip] = append(used[ip], sel.Sel.Name)
						}
					}
					return true
				})
			}
		}
	}
	paths := make([]string, 0, len(used))
	for ip := range used {
		paths = append(paths, ip)
	}
	slices.Sort(paths)
	for _, ip := range paths {
		if unusable[ip] {
			continue
		}
		ps := PackageSource{Path: ip}
		declared := map[string]bool{}
		for _, name := range RunnableStdlib[ip] {
			text, err := os.ReadFile(filepath.Join(goroot, "src", filepath.FromSlash(ip), name))
			if err != nil {
				return nil, err
			}
			f, err := parser.ParseFile(token.NewFileSet(), name, text, parser.SkipObjectResolution|parser.ParseComments)
			if err != nil {
				return nil, err
			}
			if err := checkRunnable(ip, name, f); err != nil {
				return nil, err
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						declared[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.TypeSpec:
							declared[s.Name.Name] = true
						case *ast.ValueSpec:
							for _, n := range s.Names {
								declared[n.Name] = true
							}
						}
					}
				}
			}
			ps.Files = append(ps.Files, Source{Name: name, Text: string(text)})
		}
		if !slices.ContainsFunc(used[ip], func(n string) bool { return !declared[n] }) {
			pkgs = append(pkgs, ps)
		}
	}
	return pkgs, nil
}

// checkRunnable refuses a listed file that GoCore could run differently from
// the program `go run` builds: one with a build constraint (it might not be
// the file the toolchain compiles), a `//go:linkname`, a function without a
// body (assembly), or an import of a package that is not itself runnable
// (`unsafe` included). A Go release that changes a listed file so fails the
// export loudly instead of slipping such a construct in.
func checkRunnable(ip, name string, f *ast.File) error {
	where := ip + "/" + name
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
	for _, spec := range f.Imports {
		imp, err := strconv.Unquote(spec.Path.Value)
		if err != nil || RunnableStdlib[imp] == nil {
			return fmt.Errorf("%s: imports %s, which is not runnable", where, spec.Path.Value)
		}
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body == nil {
			return fmt.Errorf("%s: %s has no body", where, fd.Name.Name)
		}
	}
	return nil
}
