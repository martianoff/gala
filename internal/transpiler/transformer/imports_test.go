package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

	// GetAlias should return the alias for package name
	alias, ok := m.GetAlias("mylib")
	assert.True(t, ok)
	assert.Equal(t, "lib", alias)
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

	path, ok := m.GetPath("std")
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

	// GetAlias should work with new name
	alias, ok := m.GetAlias("realpkg")
	assert.True(t, ok)
	assert.Equal(t, "mypkg", alias)
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
