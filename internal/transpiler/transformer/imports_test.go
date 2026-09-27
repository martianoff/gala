package transformer_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/transformer"
)

func TestImportManager_Add(t *testing.T) {
	m := transformer.NewImportManager()

	// Add a simple import with no alias
	entry := m.Add("martianoff/gala/std", "", false, "std")

	assert.Equal(t, "martianoff/gala/std", entry.Path)
	assert.Equal(t, "std", entry.PkgName)
	assert.Equal(t, "std", entry.Alias)
	assert.False(t, entry.IsDot)

	// Verify lookups work
	assert.True(t, m.IsPackage("std"))
	e, ok := m.GetByAlias("std")
	assert.True(t, ok)
	assert.Equal(t, entry, e)
}

func TestImportManager_AddWithAlias(t *testing.T) {
	m := transformer.NewImportManager()

	// Add an import with explicit alias
	entry := m.Add("pkg/mylib", "lib", false, "mylib")

	assert.Equal(t, "pkg/mylib", entry.Path)
	assert.Equal(t, "mylib", entry.PkgName)
	assert.Equal(t, "lib", entry.Alias)

	// Should be findable by alias
	assert.True(t, m.IsPackage("lib"))
	assert.False(t, m.IsPackage("mylib")) // Not by package name

	// ResolveAlias should return actual package name
	pkgName, ok := m.ResolveAlias("lib")
	assert.True(t, ok)
	assert.Equal(t, "mylib", pkgName)

	// Qualifier should return the aliased entry for the package name
	q, ok := m.Qualifier("mylib")
	assert.True(t, ok)
	assert.Equal(t, "lib", q.Alias)
}

func TestImportManager_DotImport(t *testing.T) {
	m := transformer.NewImportManager()

	// Add a dot import
	entry := m.Add("martianoff/gala/std", "", true, "std")

	assert.Equal(t, "std", entry.PkgName)
	assert.True(t, entry.IsDot)

	// Should be dot-imported
	assert.True(t, m.IsDotImported("std"))
	assert.False(t, m.IsDotImported("other"))

	// GetDotImports should return the list
	dotImports := m.GetDotImports()
	assert.Equal(t, []string{"std"}, dotImports)
}

func TestImportManager_AddFromPackages(t *testing.T) {
	m := transformer.NewImportManager()

	packages := map[string]string{
		"martianoff/gala/std":                  "std",
		"martianoff/gala/collection_immutable": "collection_immutable",
	}

	m.AddFromPackages(packages)

	assert.True(t, m.IsPackage("std"))
	assert.True(t, m.IsPackage("collection_immutable"))

	path, ok := m.PathForQualifier("std")
	assert.True(t, ok)
	assert.Equal(t, "martianoff/gala/std", path)
}

func TestImportManager_UpdateActualPackageName(t *testing.T) {
	m := transformer.NewImportManager()

	// Add with guessed package name
	m.Add("github.com/org/pkg", "mypkg", false, "pkg")

	// Update with actual package name from AST analysis
	m.UpdateActualPackageName("github.com/org/pkg", "realpkg")

	// Should resolve to new name
	pkgName, ok := m.ResolveAlias("mypkg")
	assert.True(t, ok)
	assert.Equal(t, "realpkg", pkgName)

	// Qualifier should work with new name
	q, ok := m.Qualifier("realpkg")
	assert.True(t, ok)
	assert.Equal(t, "mypkg", q.Alias)
}

func TestImportManager_DerivePkgNameFromPath(t *testing.T) {
	m := transformer.NewImportManager()

	// Add without specifying package name - should derive from path
	entry := m.Add("github.com/org/mypackage", "", false, "")

	assert.Equal(t, "mypackage", entry.PkgName)
	assert.Equal(t, "mypackage", entry.Alias)
}

func TestImportManager_GetByPath(t *testing.T) {
	m := transformer.NewImportManager()

	m.Add("pkg/a", "aliasA", false, "pkga")
	m.Add("pkg/b", "", false, "pkgb")

	entry, ok := m.GetByPath("pkg/a")
	assert.True(t, ok)
	assert.Equal(t, "aliasA", entry.Alias)

	entry, ok = m.GetByPath("pkg/b")
	assert.True(t, ok)
	assert.Equal(t, "pkgb", entry.Alias)

	_, ok = m.GetByPath("pkg/notfound")
	assert.False(t, ok)
}

func TestImportManager_MultipleDotImports(t *testing.T) {
	m := transformer.NewImportManager()

	m.Add("pkg/a", "", true, "a")
	m.Add("pkg/b", "", true, "b")
	m.Add("pkg/c", "", false, "c") // Not a dot import

	assert.True(t, m.IsDotImported("a"))
	assert.True(t, m.IsDotImported("b"))
	assert.False(t, m.IsDotImported("c"))

	dotImports := m.GetDotImports()
	assert.Len(t, dotImports, 2)
	assert.Contains(t, dotImports, "a")
	assert.Contains(t, dotImports, "b")
}

func TestImportManager_ExplicitImportOverridesImplicit(t *testing.T) {
	m := transformer.NewImportManager()

	// Simulate real flow: richAST.Packages added first (implicit imports)
	m.AddFromPackages(map[string]string{
		"martianoff/gala/std": "std",
	})

	// Then explicit import from source with alias (should override)
	m.Add("martianoff/gala/std", "mystd", false, "std")

	// The explicit import should take precedence
	entry, ok := m.GetByPath("martianoff/gala/std")
	assert.True(t, ok)
	assert.Equal(t, "mystd", entry.Alias) // Explicit import wins
}

// TestPruneUnused_DotImportGenericInstantiation verifies that a dot-imported
// package referenced ONLY through generic-type instantiations (Foo[A] /
// Foo[A, B]) keeps the import. The pruning scan must descend through
// *ast.IndexExpr and *ast.IndexListExpr to the base identifier; if it only
// matched bare *ast.Ident, the dot import would be erroneously stripped and
// the generated Go would fail with `undefined: Foo`.
func TestPruneUnused_DotImportGenericInstantiation(t *testing.T) {
	src := `package main

import . "example.com/lib"

func main() {
	_ = Container[int]{Value: 42}
	_ = Pair[string, int]{First: "x", Second: 1}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, 0)
	assert.NoError(t, err)

	m := transformer.NewImportManager()
	m.Add("example.com/lib", "", true, "lib")

	richAST := &transpiler.RichAST{
		GoExports: map[string][]string{
			"lib": {"Container", "Pair"},
		},
	}

	m.PruneUnused(file, richAST)

	// The dot import must survive pruning.
	dotImportFound := false
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gd.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok {
				continue
			}
			if imp.Name != nil && imp.Name.Name == "." && imp.Path.Value == `"example.com/lib"` {
				dotImportFound = true
			}
		}
	}
	assert.True(t, dotImportFound, "expected dot import of example.com/lib to be preserved when only generic types are referenced")
}

func TestImportManager_AddFromPackagesSkipsExistingPaths(t *testing.T) {
	m := transformer.NewImportManager()

	// First add explicit import from source
	m.Add("martianoff/gala/std", "mystd", false, "std")

	// Then AddFromPackages should skip this path
	m.AddFromPackages(map[string]string{
		"martianoff/gala/std": "std",
	})

	// The explicit import should be preserved
	entry, ok := m.GetByPath("martianoff/gala/std")
	assert.True(t, ok)
	assert.Equal(t, "mystd", entry.Alias) // First (explicit) one preserved
}

// TestImportManager_SamePathUnderTwoNames: Go allows one path to be imported
// plainly and under an alias in the same file, and both names must resolve.
// Only the implicit seed from AddFromPackages is replaced by an explicit import.
func TestImportManager_SamePathUnderTwoNames(t *testing.T) {
	m := transformer.NewImportManager()
	m.AddFromPackages(map[string]string{"martianoff/gala/strings": "strings"})
	m.Add("martianoff/gala/strings", "", false, "strings")
	m.Add("martianoff/gala/strings", "gs", false, "strings")

	for _, alias := range []string{"strings", "gs"} {
		entry, ok := m.GetByAlias(alias)
		if assert.True(t, ok, alias) {
			assert.Equal(t, "martianoff/gala/strings", entry.Path)
		}
	}
	entry, ok := m.GetByPath("martianoff/gala/strings")
	assert.True(t, ok)
	assert.Equal(t, "strings", entry.Alias, "GetByPath keeps the first explicit import")
	assert.Len(t, m.All(), 2, "the implicit seed is replaced, the two explicit imports stay")
}

// TestImportManager_GalaImportOwnsSharedName: when a GALA import and a Go
// import share a package name, Qualifier — which maps a GALA metadata package
// name back to this file's qualifier — must answer with the GALA import's
// alias whatever the declaration order.
func TestImportManager_GalaImportOwnsSharedName(t *testing.T) {
	for _, galaFirst := range []bool{true, false} {
		m := transformer.NewImportManager()
		m.AddFromPackages(map[string]string{"martianoff/gala/strings": "strings"})
		if galaFirst {
			m.Add("martianoff/gala/strings", "gs", false, "strings")
			m.Add("strings", "", false, "")
		} else {
			m.Add("strings", "", false, "")
			m.Add("martianoff/gala/strings", "gs", false, "strings")
		}
		m.UpdateActualPackageName("martianoff/gala/strings", "strings")
		m.ClaimGalaPackageNames(map[string]bool{"martianoff/gala/strings": true})

		q, ok := m.Qualifier("strings")
		if assert.True(t, ok) {
			assert.Equal(t, "gs", q.Alias, "galaFirst=%v", galaFirst)
		}
		goEntry, ok := m.GetByAlias("strings")
		if assert.True(t, ok) {
			assert.Equal(t, "strings", goEntry.Path, "the Go import still answers to its own name")
		}
	}
}

// TestImportManager_ImplicitGalaPackageGetsFreeQualifier: a GALA package this
// file does not import (it reached the file through a sibling's imports) still
// owns its package name for GALA lookups, but its qualifier must not collide
// with a Go import of the same name that the file does declare.
func TestImportManager_ImplicitGalaPackageGetsFreeQualifier(t *testing.T) {
	m := transformer.NewImportManager()
	m.AddFromPackages(map[string]string{"martianoff/gala/strings": "strings"})
	m.Add("strings", "", false, "")
	m.UpdateActualPackageName("martianoff/gala/strings", "strings")
	m.ClaimGalaPackageNames(map[string]bool{"martianoff/gala/strings": true})

	q, ok := m.Qualifier("strings")
	if assert.True(t, ok) {
		assert.Equal(t, "martianoff/gala/strings", q.Path)
		assert.Equal(t, "gala_strings", q.Alias)
		assert.True(t, q.Implicit())
	}
	goEntry, ok := m.GetByAlias("strings")
	if assert.True(t, ok) {
		assert.Equal(t, "strings", goEntry.Path)
	}
}

// TestImportManager_TransitiveQualifier: a Go package the file does not import
// gets its own name unless an import or another transitive import binds it.
func TestImportManager_TransitiveQualifier(t *testing.T) {
	m := transformer.NewImportManager()
	m.Add("martianoff/gala/fs", "", false, "fs")
	assert.Equal(t, "fs2", m.TransitiveQualifier("io/fs", "fs"))
	m.AddTransitive("io/fs", "fs2")
	assert.Equal(t, "fs2", m.TransitiveQualifier("io/fs", "fs"), "a path keeps the qualifier chosen for it")
	assert.Equal(t, "io", m.TransitiveQualifier("io", "io"))
}

// TestImportManager_UnaliasedGoImportNames: an unaliased Go import answers to
// its real package name when the analyzer learned it. Otherwise it answers to
// every name its path may bind, since a `/vN` suffix does not settle the name
// (math/rand/v2 is package rand, k8s.io/api/core/v1 is package v1), and a
// guessed name never shadows a surer binding.
func TestImportManager_UnaliasedGoImportNames(t *testing.T) {
	cases := []struct {
		path, realName string
		unknownNames   []string
	}{
		{"k8s.io/api/core/v1", "v1", []string{"v1", "core"}},
		{"math/rand/v2", "rand", []string{"v2", "rand"}},
		{"gopkg.in/yaml.v3", "yaml", []string{"yaml"}},
		{"github.com/mattn/go-sqlite3", "sqlite3", []string{"sqlite3"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			known := transformer.NewImportManager()
			known.Add(tc.path, "", false, tc.realName)
			entry, ok := known.GetByAlias(tc.realName)
			if assert.True(t, ok) {
				assert.Equal(t, tc.path, entry.Path)
				assert.Equal(t, tc.realName, entry.QualifierFor(tc.realName))
			}

			unknown := transformer.NewImportManager()
			unknown.Add(tc.path, "", false, "")
			for _, name := range tc.unknownNames {
				entry, ok := unknown.GetByAlias(name)
				if assert.True(t, ok, name) {
					assert.Equal(t, tc.path, entry.Path)
				}
			}
		})
	}

	// A guessed `core` from the k8s path does not shadow a package that
	// really is `core`, declared after it.
	m := transformer.NewImportManager()
	m.Add("k8s.io/api/core/v1", "", false, "")
	m.Add("example.com/core", "", false, "core")
	entry, _ := m.GetByAlias("core")
	assert.Equal(t, "example.com/core", entry.Path)
	entry, _ = m.GetByAlias("v1")
	assert.Equal(t, "k8s.io/api/core/v1", entry.Path)

	aliased := transformer.NewImportManager().Add("math/rand/v2", "r2", false, "")
	assert.Equal(t, "r2", aliased.QualifierFor("rand"), "a written alias wins")
}
