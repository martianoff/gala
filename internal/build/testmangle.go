package build

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A transpiled test file has a _test.go name, so Go compiles it only into its
// own package's test and no importer sees its declarations. In a _test.go file,
// `go test` takes every top-level TestXxx, BenchmarkXxx and FuzzXxx function
// for a Go test and rejects it unless its signature is func(*testing.T) (or
// *testing.B, *testing.F). A GALA test is func TestXxx(t T) T, so those
// functions are emitted under goTestFuncName instead, and the generated harness
// calls them by that name while reporting the GALA one.

// goTestFuncName is the Go name of the GALA function name in a transpiled
// _test.go file, when go test would take name for one of its own.
func goTestFuncName(name string) string {
	return "gala_" + name
}

// isGoTestName mirrors cmd/go's isTest for the prefixes whose signature it
// checks: the prefix, then nothing or a character that is not lowercase.
func isGoTestName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			r, _ := utf8.DecodeRuneInString(rest)
			if rest == "" || !unicode.IsLower(r) {
				return true
			}
		}
	}
	return false
}

// testOutputsByDir groups the transpiled outputs of testFiles that have a
// _test.go name by the gen directory they are in. Outputs written under
// another name (a main root's tests, built into its test binary) are left out.
func (b *Builder) testOutputsByDir(testFiles []string) map[string][]string {
	byDir := make(map[string][]string)
	for _, tf := range testFiles {
		out := filepath.Join(b.workspace.GenDir, testGenFileName(b.workspace.ProjectDir, tf))
		if strings.HasSuffix(out, "_test.go") && fileExists(out) {
			byDir[filepath.Dir(out)] = append(byDir[filepath.Dir(out)], out)
		}
	}
	return byDir
}

// renameGoTestFuncs renames, across paths (the transpiled test files of one
// package), every top-level function isGoTestName matches to goTestFuncName,
// and every reference to one. The names are replaced in place, so the files'
// //line directives still map them to their .gala source.
func renameGoTestFuncs(paths []string) error {
	type parsed struct {
		path string
		src  []byte
		file *ast.File
	}
	fset := token.NewFileSet()
	files := make([]parsed, 0, len(paths))
	renamed := make(map[string]bool)
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", p, err)
		}
		files = append(files, parsed{p, src, f})
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && isGoTestName(fn.Name.Name) {
				renamed[fn.Name.Name] = true
			}
		}
	}
	if len(renamed) == 0 {
		return nil
	}

	for _, pf := range files {
		// ast.Inspect visits the identifiers in source order.
		refs := packageFuncRefs(pf.file, renamed)
		if len(refs) == 0 {
			continue
		}
		var sb strings.Builder
		last := 0
		for _, id := range refs {
			off := fset.Position(id.Pos()).Offset
			sb.Write(pf.src[last:off])
			sb.WriteString(goTestFuncName(id.Name))
			last = off + len(id.Name)
		}
		sb.Write(pf.src[last:])
		if err := os.WriteFile(pf.path, []byte(sb.String()), 0644); err != nil {
			return err
		}
	}
	return nil
}

// packageFuncRefs returns the identifiers in f that name one of the package's
// top-level functions in names: their declarations and their uses. A name a
// local declaration shadows, a field, a method and a selector's member are not.
func packageFuncRefs(f *ast.File, names map[string]bool) []*ast.Ident {
	notFunc := make(map[*ast.Ident]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Recv != nil {
				notFunc[n.Name] = true
			}
		case *ast.Field:
			for _, id := range n.Names {
				notFunc[id] = true
			}
		case *ast.SelectorExpr:
			notFunc[n.Sel] = true
		case *ast.KeyValueExpr:
			// A composite literal's key is a field name; a function value is
			// not comparable, so it is never a map key.
			if id, ok := n.Key.(*ast.Ident); ok {
				notFunc[id] = true
			}
		}
		return true
	})
	var refs []*ast.Ident
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if ok && names[id.Name] && !notFunc[id] && (id.Obj == nil || id.Obj.Kind == ast.Fun) {
			refs = append(refs, id)
		}
		return true
	})
	return refs
}
