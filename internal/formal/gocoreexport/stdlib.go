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
// Go's own source, by import path: small files that import only runnable or
// quiet packages (checkRunnable), so the program runs the very code `go run`
// links, not a model of it. It is reviewed like GoCore's library models: a file
// is added here only with the guards of checkRunnable and checkSiblings
// passing.
var RunnableStdlib = map[string][]string{
	"errors":       {"errors.go"},
	"cmp":          {"cmp.go"},
	"math/bits":    {"bits.go", "bits_tables.go"},
	"slices":       {"sort.go", "zsortanyfunc.go"},
	"strconv":      {"atoi.go", "isprint.go", "quote.go"},
	"unicode/utf8": {"utf8.go"},
}

// AddStdlib returns pkgs with the packages of RunnableStdlib the program
// imports (not with `.` or `_`), and their runnable imports, their listed files
// read from goroot, marked partial: a name the program uses that those files do
// not declare is one GoCore reports as outside what it runs. The error is for a file that cannot
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
	// A runnable package's own runnable imports come with it, each before the
	// packages that import it (strconv's atoi.go initializes ErrRange with
	// errors.New).
	added := map[string]bool{}
	var add func(ip string) error
	add = func(ip string) error {
		if added[ip] {
			return nil
		}
		added[ip] = true
		ps, err := readRunnable(goroot, ip)
		if err != nil {
			return err
		}
		for _, src := range ps.Files {
			f, err := parser.ParseFile(token.NewFileSet(), src.Name, src.Text, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range f.Imports {
				if dep, err := strconv.Unquote(spec.Path.Value); err == nil && RunnableStdlib[dep] != nil {
					if err := add(dep); err != nil {
						return err
					}
				}
			}
		}
		pkgs = append(pkgs, ps)
		return nil
	}
	for _, ip := range paths {
		if err := add(ip); err != nil {
			return nil, err
		}
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
		if err := checkRunnable(goroot, ip+"/"+name, f); err != nil {
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
// body (assembly), or an import other than of a runnable package or of a
// quiet one (quietImport), `unsafe` never. A Go release that changes a listed
// file so fails the export loudly instead of slipping such a construct in.
func checkRunnable(goroot, where string, f *ast.File) error {
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
		ip, err := strconv.Unquote(spec.Path.Value)
		if err != nil || ip == "unsafe" || spec.Name != nil {
			return fmt.Errorf("%s: imports %s", where, spec.Path.Value)
		}
		if RunnableStdlib[ip] == nil {
			if err := quietImport(goroot, ip); err != nil {
				return fmt.Errorf("%s: imports %s: %w", where, ip, err)
			}
		}
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
// type's method set), or by changing a package-level variable a listed file
// declares. A sibling may name only a sealed one, an error from errors.New,
// whose value no other package can change (errorString's field is
// unexported), and never write it (writes); another may be reached through an
// alias (`t := v; t[0] = x`), so naming it at all is refused. What else those
// files do while they initialize, GoCore leaves out as for any quiet package:
// every runnable package is in quietInitPackages (TestRunnableStdlibIsQuiet).
func checkSiblings(ip, dir string, listed []*ast.File) error {
	types, vars, sealed := map[string]bool{}, map[string]bool{}, map[string]bool{}
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
							if len(s.Names) == 1 && len(s.Values) == 1 {
								if call, ok := s.Values[0].(*ast.CallExpr); ok {
									if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "New" {
										if x, ok := sel.X.(*ast.Ident); ok && x.Name == "errors" {
											sealed[s.Names[0].Name] = true
										}
									}
								}
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
				if vars[n.Name] && !sealed[n.Name] && bad == nil {
					bad = fmt.Errorf("%s/%s: names %s, a variable of a runnable file", ip, name, n.Name)
				}
			}
			for _, w := range writes(n) {
				if id := rootIdent(w); id != nil && vars[id.Name] && bad == nil {
					bad = fmt.Errorf("%s/%s: may write %s, a variable of a runnable file", ip, name, id.Name)
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

// writes is what node n may write: the left side of an assignment, an
// operand whose address it takes or that it increments, a range clause's key
// and value, and the receiver of a method call (a pointer method takes its
// address). Reading a variable cannot change what the listed files do; any
// write to one, through a field, an index or a pointer too, could.
func writes(n ast.Node) []ast.Expr {
	switch n := n.(type) {
	case *ast.AssignStmt:
		return n.Lhs
	case *ast.IncDecStmt:
		return []ast.Expr{n.X}
	case *ast.UnaryExpr:
		if n.Op == token.AND {
			return []ast.Expr{n.X}
		}
	case *ast.RangeStmt:
		return []ast.Expr{n.Key, n.Value}
	case *ast.CallExpr:
		if s, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok {
			return []ast.Expr{s.X}
		}
	}
	return nil
}

// rootIdent is the variable an expression reads or writes through selectors, type
// assertions, indexing, slicing and dereferences, or nil for any other expression.
func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.TypeAssertExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// quietImport checks a package a runnable file imports that GoCore does not
// hold, whose initialization GoCore leaves out. Its own initialization does
// nothing: in every file of its directory (build constraints included, the
// stricter reading), there is no init function and no package-level variable.
// And it is one of fmt's dependencies, so what its own imports do as they
// initialize is part of fmt's initialization, which every program GoCore runs
// has (GALA's std imports fmt) and which quietInitPackages already takes to
// print nothing and not panic. Every name of it the program reaches stays
// unsupported (partial packages), but for GoCore's models.
func quietImport(goroot, ip string) error {
	deps, err := stdDeps(goroot, "fmt")
	if err != nil {
		return err
	}
	if !deps[ip] {
		return fmt.Errorf("not a dependency of fmt")
	}
	names, err := filepath.Glob(filepath.Join(goroot, "src", filepath.FromSlash(ip), "*.go"))
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no source in GOROOT")
	}
	for _, path := range names {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" {
					return fmt.Errorf("%s has an init function", filepath.Base(path))
				}
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					return fmt.Errorf("%s declares a package-level variable", filepath.Base(path))
				}
			}
		}
	}
	return nil
}

// stdDeps is the standard-library packages root imports, directly or not, in
// goroot, with root: the imports of every non-test file of each, whatever its
// build constraints (a superset of what one build links).
func stdDeps(goroot, root string) (map[string]bool, error) {
	seen := map[string]bool{root: true}
	queue := []string{root}
	for len(queue) > 0 {
		ip := queue[0]
		queue = queue[1:]
		names, err := filepath.Glob(filepath.Join(goroot, "src", filepath.FromSlash(ip), "*.go"))
		if err != nil {
			return nil, err
		}
		for _, path := range names {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return nil, err
			}
			for _, spec := range f.Imports {
				if dep, err := strconv.Unquote(spec.Path.Value); err == nil && !seen[dep] {
					seen[dep] = true
					queue = append(queue, dep)
				}
			}
		}
	}
	return seen, nil
}
