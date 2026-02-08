package module

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindModuleRoot(t *testing.T) {
	// Create a temp directory with go.mod
	tempDir, err := os.MkdirTemp("", "find_module_root_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	goModContent := "module martianoff/gala\n\ngo 1.21\n"
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create a subdirectory to search from
	subDir := filepath.Join(tempDir, "internal", "pkg")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	moduleRoot, moduleName := FindModuleRoot(subDir)

	assert.Equal(t, tempDir, moduleRoot, "should find module root")
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
	// Create a temp directory with go.mod
	tempDir, err := os.MkdirTemp("", "new_resolver_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	goModContent := "module martianoff/gala\n\ngo 1.21\n"
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)

	// Change to temp directory
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	resolver := NewResolver(nil)

	assert.NotEmpty(t, resolver.ModuleRoot())
	assert.Equal(t, "martianoff/gala", resolver.ModuleName())
}

func TestResolver_ResolvePackagePath_ModuleRelative(t *testing.T) {
	// Create a temp directory with go.mod and a package dir
	tempDir, err := os.MkdirTemp("", "resolve_module_relative_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	goModContent := "module martianoff/gala\n\ngo 1.21\n"
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create a "std" package directory
	stdDir := filepath.Join(tempDir, "std")
	err = os.MkdirAll(stdDir, 0755)
	require.NoError(t, err)

	// Change to temp directory
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	require.NoError(t, err)

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
	// Create a temp directory with go.mod and a package dir
	tempDir, err := os.MkdirTemp("", "resolve_simple_name_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	goModContent := "module martianoff/gala\n\ngo 1.21\n"
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create a "std" package directory
	err = os.MkdirAll(filepath.Join(tempDir, "std"), 0755)
	require.NoError(t, err)

	// Change to temp directory
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	require.NoError(t, err)

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

func TestResolver_HasGalaMod(t *testing.T) {
	// Create a temp directory with gala.mod
	tempDir, err := os.MkdirTemp("", "resolver_galamod_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create go.mod
	goModContent := "module test/project\n\ngo 1.21\n"
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create gala.mod
	galaModContent := "module test/project\n\ngala 1.0\n"
	err = os.WriteFile(filepath.Join(tempDir, "gala.mod"), []byte(galaModContent), 0644)
	require.NoError(t, err)

	// Change to temp directory for the test
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	resolver := NewResolver(nil)
	assert.True(t, resolver.HasGalaMod())
	assert.NotNil(t, resolver.GalaMod())
	assert.Equal(t, "test/project", resolver.GalaMod().Module.Path)
}

func TestResolver_ReplaceDirective_LocalPath(t *testing.T) {
	// Create a temp directory structure
	tempDir, err := os.MkdirTemp("", "resolver_replace_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create project directory
	projectDir := filepath.Join(tempDir, "project")
	err = os.MkdirAll(projectDir, 0755)
	require.NoError(t, err)

	// Create local replacement package
	localPkgDir := filepath.Join(tempDir, "local-utils")
	err = os.MkdirAll(localPkgDir, 0755)
	require.NoError(t, err)

	// Create go.mod in project
	goModContent := "module test/project\n\ngo 1.21\n"
	err = os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte(goModContent), 0644)
	require.NoError(t, err)

	// Create gala.mod with replace directive
	galaModContent := `module test/project

gala 1.0

replace github.com/example/utils => ../local-utils
`
	err = os.WriteFile(filepath.Join(projectDir, "gala.mod"), []byte(galaModContent), 0644)
	require.NoError(t, err)

	// Change to project directory
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(projectDir)
	require.NoError(t, err)

	resolver := NewResolver(nil)
	require.True(t, resolver.HasGalaMod())

	// Should resolve to local path
	path, err := resolver.ResolvePackagePath("github.com/example/utils")
	require.NoError(t, err)
	assert.Equal(t, localPkgDir, filepath.Clean(path))
}
