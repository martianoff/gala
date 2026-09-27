package transformer

import (
	"go/ast"
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// TestTypeToExprByImportPath covers a type that carries its import path, as Go
// types from the Go SDK and types written against a Go import do: it resolves
// through that path, never through a lookup by package name.
func TestTypeToExprByImportPath(t *testing.T) {
	fixture := func() *galaASTTransformer {
		tr := NewGalaASTTransformer().(*galaASTTransformer)
		tr.packageName = "main"
		tr.importManager = NewImportManager()
		tr.richAST = &transpiler.RichAST{}
		return tr
	}
	selector := func(t *testing.T, e ast.Expr) string {
		t.Helper()
		sel, ok := e.(*ast.SelectorExpr)
		if !assert.True(t, ok, "want a qualified type, got %#v", e) {
			return ""
		}
		return sel.X.(*ast.Ident).Name + "." + sel.Sel.Name
	}

	t.Run("an unaliased Go import qualifies with the package's real name", func(t *testing.T) {
		cases := []struct{ path, pkg, want string }{
			{"math/rand/v2", "rand", "rand.Rand"},
			{"gopkg.in/yaml.v3", "yaml", "yaml.Node"},
			{"github.com/mattn/go-sqlite3", "sqlite3", "sqlite3.Conn"},
		}
		for _, tc := range cases {
			tr := fixture()
			tr.importManager.Add(tc.path, "", false, "")
			name := tc.want[len(tc.pkg)+1:]
			got := tr.typeToExpr(transpiler.NamedType{Package: tc.pkg, Name: name, ImportPath: tc.path})
			assert.Equal(t, tc.want, selector(t, got), tc.path)
		}
	})

	t.Run("a dot import of a same-name GALA package does not strip an unimported Go type", func(t *testing.T) {
		tr := fixture()
		tr.importManager.Add("martianoff/gala/fs", "", true, "fs")
		tr.importManager.Add("os", "", false, "")
		got := tr.typeToExpr(transpiler.NamedType{Package: "fs", Name: "FileInfo", ImportPath: "io/fs"})
		assert.Equal(t, "fs.FileInfo", selector(t, got))
		assert.Equal(t, "fs", tr.importManager.GetTransitiveImports()["io/fs"], "io/fs must be imported")
	})

	t.Run("a dot-imported path emits the bare name and keeps the import", func(t *testing.T) {
		tr := fixture()
		tr.importManager.Add("strings", "", true, "")
		got := tr.typeToExpr(transpiler.NamedType{Package: "strings", Name: "Builder", ImportPath: "strings"})
		assert.Equal(t, &ast.Ident{Name: "Builder"}, got)
		assert.True(t, tr.importManager.usedDotImports["strings"], "the dot import must be marked used so pruning keeps it")
	})
}
