package build

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// A transpiled test file has a _test.go name, so Go compiles it only into its
// own package's test and no importer sees its declarations. In a _test.go file,
// `go test` takes every top-level TestXxx, BenchmarkXxx and FuzzXxx function
// for a Go test and rejects it unless its signature is func(*testing.T) (or
// *testing.B, *testing.F), and vet checks ExampleXxx functions too. A GALA test
// is func TestXxx(t T) T, so those functions are emitted under goTestFuncName
// instead, and the generated harness calls them by that name while reporting
// the GALA one.

const goTestFuncPrefix = "gala_"

// goTestFuncName is the Go name of the top-level GALA function name in a
// transpiled _test.go file: name itself, unless go test would take it for one
// of its own tests (isGoTestName).
func goTestFuncName(name string) string {
	if isGoTestName(name) {
		return goTestFuncPrefix + name
	}
	return name
}

// isGoTestName reports whether go test would check name in a _test.go file:
// cmd/go rejects a TestXxx, BenchmarkXxx or FuzzXxx with the wrong signature,
// and the vet `tests` analyzer it runs checks every function whose name merely
// starts with Test, Benchmark, Fuzz or Example. The plain prefix covers both.
func isGoTestName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// renameGoTestFuncsByPackage runs renameGoTestFuncs over the transpiled
// outputs of testFiles, one package (gen directory) at a time.
func (b *Builder) renameGoTestFuncsByPackage(testFiles []string) error {
	byDir := make(map[string][]string)
	for _, tf := range testFiles {
		out := filepath.Join(b.workspace.GenDir, testGenFileName(b.workspace.ProjectDir, tf))
		byDir[filepath.Dir(out)] = append(byDir[filepath.Dir(out)], out)
	}
	for _, outs := range byDir {
		if err := renameGoTestFuncs(outs); err != nil {
			return err
		}
	}
	return nil
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
		refs := packageFuncRefs(pf.file, renamed)
		if len(refs) == 0 {
			continue
		}
		tf := fset.File(pf.file.Pos())
		var buf bytes.Buffer
		buf.Grow(len(pf.src) + len(refs)*len(goTestFuncPrefix))
		last := 0
		for _, id := range refs {
			off := tf.Offset(id.Pos())
			buf.Write(pf.src[last:off])
			buf.WriteString(goTestFuncName(id.Name))
			last = off + len(id.Name)
		}
		buf.Write(pf.src[last:])
		if err := os.WriteFile(pf.path, buf.Bytes(), 0644); err != nil {
			return err
		}
	}
	return nil
}

// packageFuncRefs returns, in source order, the identifiers in f that name one
// of the package's top-level functions in names: their declarations and their
// uses. A name a local declaration shadows, a field, a method and a selector's
// member are not.
func packageFuncRefs(f *ast.File, names map[string]bool) []*ast.Ident {
	// ast.Inspect visits a node before its children, so an identifier is
	// marked here before the walk reaches it.
	notFunc := make(map[*ast.Ident]bool)
	var refs []*ast.Ident
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
		case *ast.Ident:
			if names[n.Name] && !notFunc[n] && (n.Obj == nil || n.Obj.Kind == ast.Fun) {
				refs = append(refs, n)
			}
		}
		return true
	})
	return refs
}
