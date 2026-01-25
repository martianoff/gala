package module

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindModuleRoot(t *testing.T) {
	// Get the actual module root (this test runs from within the module)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	moduleRoot, moduleName := FindModuleRoot(cwd)

	// Should find the gala module
	assert.NotEmpty(t, moduleRoot, "should find module root")
	assert.Equal(t, "martianoff/gala", moduleName, "should find correct module name")

	// Module root should contain go.mod
	goModPath := filepath.Join(moduleRoot, "go.mod")
	_, err = os.Stat(goModPath)
	assert.NoError(t, err, "module root should contain go.mod")
}

func TestFindModuleRoot_NonExistentPath(t *testing.T) {
	moduleRoot, moduleName := FindModuleRoot("/nonexistent/path/that/does/not/exist")

	assert.Empty(t, moduleRoot)
	assert.Empty(t, moduleName)
}

func TestNewResolver(t *testing.T) {
	resolver := NewResolver(nil)

	// Should find the module from cwd
	assert.NotEmpty(t, resolver.ModuleRoot())
	assert.Equal(t, "martianoff/gala", resolver.ModuleName())
}

func TestResolver_ResolvePackagePath_ModuleRelative(t *testing.T) {
	resolver := NewResolver(nil)
	require.NotEmpty(t, resolver.ModuleRoot(), "test requires module root to be found")

	// Resolve full module path
	path, err := resolver.ResolvePackagePath("martianoff/gala/std")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolver.ModuleRoot(), "std"), path)

	// Verify directory exists
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestResolver_ResolvePackagePath_SimpleName(t *testing.T) {
	resolver := NewResolver(nil)
	require.NotEmpty(t, resolver.ModuleRoot(), "test requires module root to be found")

	// Resolve simple package name
	path, err := resolver.ResolvePackagePath("std")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolver.ModuleRoot(), "std"), path)
}

func TestResolver_ResolvePackagePath_NotFound(t *testing.T) {
	resolver := NewResolver(nil)

	_, err := resolver.ResolvePackagePath("nonexistent/package/path")
	assert.Error(t, err)

	var notFoundErr *PackageNotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "nonexistent/package/path", notFoundErr.ImportPath)
}

func TestResolver_ResolvePackagePath_SearchPaths(t *testing.T) {
	// Create a temp directory to use as search path
	tempDir, err := os.MkdirTemp("", "resolver_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a fake package directory
	fakePkgDir := filepath.Join(tempDir, "fakepkg")
	err = os.Mkdir(fakePkgDir, 0755)
	require.NoError(t, err)

	// Create resolver with the temp dir as search path
	resolver := NewResolver([]string{tempDir})

	// Should find the fake package via search path
	path, err := resolver.ResolvePackagePath("fakepkg")
	require.NoError(t, err)
	assert.Equal(t, fakePkgDir, path)
}

func TestPackageNotFoundError(t *testing.T) {
	err := &PackageNotFoundError{ImportPath: "some/path"}
	assert.Equal(t, "package not found: some/path", err.Error())
}
