package gala2lean

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/stdlib"
)

// TestStdNamesOnlyInStd checks the fact the translation's rule for std's
// Some, None, Left, Right and its Option, Either and tuple types rests on:
// no package of GALA's standard library but std declares one of those
// names, so no dot import of another of them can mean another one. (A file
// that dot-imported std and such a package would not compile in Go either:
// the name would be declared twice in its file block.) Unlike the library
// functions (hideLibNames), these names keep a program-wide rule. A
// package of the program's own module, dot-imported, is reported
// (dot-import-module); a package from outside GALA's tree is an import
// outside the subset, reported where its names are used.
//
// It reads the Go every stdlib package is shipped as, which is what Go
// resolves a dot import against: its exported package-level names.
func TestStdNamesOnlyInStd(t *testing.T) {
	var names []string
	for x := range stdCtors {
		names = append(names, x)
	}
	for x, st := range stdGeneric {
		if !st.collections {
			names = append(names, x)
		}
	}
	slices.Sort(names)
	require.Subset(t, names, []string{"Some", "None", "Left", "Right", "Option", "Either", "Tuple", "Tuple3", "Tuple4"})

	inStd := map[string]bool{}
	var read []string
	for _, pkg := range stdlib.Packages() {
		read = append(read, pkg.ImportPath)
		files := stdlib.EmbeddedPackages[pkg.Name]
		require.NotEmpty(t, files, pkg.Name)
		declared := map[string]bool{}
		for file, src := range files {
			if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
			require.NoError(t, err, "%s/%s", pkg.Name, file)
			for _, x := range exportedNames(f) {
				declared[x] = true
			}
		}
		for _, x := range names {
			if pkg.ImportPath == stdPath {
				inStd[x] = declared[x]
				continue
			}
			require.False(t, declared[x], "%s declares %s, which a dot import of it would bring in", pkg.ImportPath, x)
		}
	}
	// The test reads the packages it means to: the names are std's, and the
	// packages a program dot-imports beside it are there.
	require.Subset(t, read, []string{stdPath, collectionsPath, "martianoff/gala/collection_mutable",
		"martianoff/gala/go_interop", "martianoff/gala/concurrent"})
	for _, x := range names {
		require.True(t, inStd[x], "std does not declare %s", x)
	}
}

// exportedNames are the exported package-level names Go file f declares.
func exportedNames(f *ast.File) []string {
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.IsExported() {
				out = append(out, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out = append(out, s.Name.Name)
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							out = append(out, n.Name)
						}
					}
				}
			}
		}
	}
	return out
}
