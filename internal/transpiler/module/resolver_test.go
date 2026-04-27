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

	goModContent := "module martianoff/gala\n\ngo 1.22\n"
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

	goModContent := "module martianoff/gala\n\ngo 1.22\n"
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

	goModContent := "module martianoff/gala\n\ngo 1.22\n"
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

	goModContent := "module martianoff/gala\n\ngo 1.22\n"
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
	goModContent := "module test/project\n\ngo 1.22\n"
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
	goModContent := "module test/project\n\ngo 1.22\n"
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

// TestResolver_IsGalaPackage_LocalReplace verifies that a require + replace
// pointing at a local directory containing .gala files (or a gala.mod) is
// classified as a GALA package. Without this, cross-module GALA dependencies
// served via local replace would be misclassified as Go packages because the
// cache lookup fails for never-fetched modules.
func TestResolver_IsGalaPackage_LocalReplace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "resolver_xmod_local_replace_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Local GALA library with gala.mod and a .gala source file.
	libDir := filepath.Join(tempDir, "qa_lib")
	require.NoError(t, os.MkdirAll(libDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "gala.mod"),
		[]byte("module example.com/qalib\n\ngala dev\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "effects.gala"),
		[]byte("package qalib\n"), 0644))

	// Consumer with gala.mod that requires the lib via local replace.
	consumerDir := filepath.Join(tempDir, "qa_consumer")
	require.NoError(t, os.MkdirAll(consumerDir, 0755))
	galaModContent := `module example.com/qaconsumer

gala dev

require example.com/qalib v0.0.0

replace example.com/qalib => ../qa_lib
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "gala.mod"),
		[]byte(galaModContent), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	require.NoError(t, os.Chdir(consumerDir))

	resolver := NewResolver(nil)
	require.True(t, resolver.HasGalaMod(),
		"consumer's gala.mod must be loaded — otherwise replace directives are invisible")
	require.Equal(t, "example.com/qaconsumer", resolver.ModuleName(),
		"module name must come from gala.mod, not a stray parent go.mod")

	// The consumer's GALA dependency, served via local replace, must classify
	// as a GALA package. If this returns false the analyzer routes the dep
	// through AnalyzeGoPackage and drops sealed-type Apply method metadata,
	// causing the transformer to emit a bare conversion call on the consumer
	// side instead of `Type[T]{}.Apply(...)`.
	assert.True(t, resolver.IsGalaPackage("example.com/qalib"),
		"locally replaced GALA dependency must be recognised as a GALA package")
}

// TestResolver_PrefersGalaModOverParentGoMod verifies that when a project
// directory has gala.mod but lives under an unrelated parent go.mod, the
// resolver picks the project's gala.mod as the authoritative module root
// rather than the parent go.mod. This ensures the project's replace and
// require directives are loaded.
func TestResolver_PrefersGalaModOverParentGoMod(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "resolver_prefer_galamod_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Unrelated parent go.mod above the project — simulates running gala
	// build inside e.g. /tmp/my_project where /tmp/go.mod exists.
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "go.mod"),
		[]byte("module unrelated/parent\n\ngo 1.22\n"), 0644))

	// Project directory: only gala.mod, no go.mod.
	projectDir := filepath.Join(tempDir, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/project\n\ngala dev\n"), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	require.NoError(t, os.Chdir(projectDir))

	resolver := NewResolver(nil)
	require.True(t, resolver.HasGalaMod(),
		"project's gala.mod must be loaded, not the parent go.mod")
	assert.Equal(t, "example.com/project", resolver.ModuleName(),
		"module name must come from gala.mod (example.com/project), not parent go.mod (unrelated/parent)")
	assert.Equal(t, projectDir, resolver.ModuleRoot(),
		"module root must be the project directory, not the parent")
}

// TestResolver_IsGalaPackage_RequireWithoutCache_FallsThroughToSearchPath
// captures the failure mode that breaks gala-server's cross-module Bazel build
// (the BUG-10/BUG-15/BUG-16 trio). The consumer's gala.mod declares
//
//	require example.com/dep v0.1.0
//
// without a `replace` directive — Bazel's local_path_override materialises the
// dep at execroot/external/<repo>+/, never populating the GALA cache at
// ~/.gala/cache. The dep is therefore reachable only through a search path.
//
// Before the fix, IsGalaPackage hit the require entry, called
// isGalaPackageInCache, got back false (cache empty), and returned false
// straight away. The analyzer then routed the import through
// AnalyzeGoPackage and discarded the dep's sealed-case and struct field
// metadata, so the transformer emitted bare `Case[T]()` conversions and
// `Struct{Field: rawValue}` literals on the consumer side.
//
// The fix lets the require check fall through to the search-path module-root
// scan when isGalaPackageInCache returns false. The dep's gala.mod is then
// found via the search path and the import is correctly classified as GALA.
func TestResolver_IsGalaPackage_RequireWithoutCache_FallsThroughToSearchPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "resolver_require_no_cache_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// External GALA dep — has gala.mod and at least one .gala source.
	// Simulates execroot/external/dep+/ under Bazel's local_path_override.
	depDir := filepath.Join(tempDir, "external_dep")
	require.NoError(t, os.MkdirAll(depDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(depDir, "gala.mod"),
		[]byte("module example.com/dep\n\ngala dev\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(depDir, "lib.gala"),
		[]byte("package dep\n"), 0644))

	// Consumer with gala.mod that REQUIRES the dep but does NOT replace it.
	// Under Bazel this is the realistic shape: bazel_dep + local_path_override
	// supply the dep at link time, gala.mod just records the version.
	consumerDir := filepath.Join(tempDir, "consumer")
	require.NoError(t, os.MkdirAll(consumerDir, 0755))
	galaModContent := `module example.com/consumer

gala dev

require example.com/dep v0.1.0
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "gala.mod"),
		[]byte(galaModContent), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	require.NoError(t, os.Chdir(consumerDir))

	// Search paths mirror what gala_transpile produces in real builds:
	// the consumer's project root is the first entry (so its gala.mod wins
	// the findGalaModRoot scan), with the dep dir appended after — see
	// gala.bzl's _dep_search_shell_prelude and the --search assembly in
	// gala_transpile / gala_transpile_package.
	resolver := NewResolver([]string{consumerDir, depDir})
	require.True(t, resolver.HasGalaMod(),
		"consumer's gala.mod must be loaded for the require entry to be visible")
	require.Equal(t, "example.com/consumer", resolver.ModuleName())

	// The dep must classify as a GALA package even though it isn't in the
	// cache — because the search path leads to a directory whose gala.mod
	// matches the import path. If this is false the analyzer routes the
	// import through AnalyzeGoPackage and the consumer regresses to bare
	// `Case[T]()` conversions for sealed-case constructors.
	assert.True(t, resolver.IsGalaPackage("example.com/dep"),
		"GALA dep required without a cached version must still classify as GALA when reachable via a search path")
}

// TestResolver_PrefersSearchPathGalaModOverCwdGalaMod captures the
// gala_bootstrap-from-downstream-execroot scenario. Reproduces the original
// failure where transpiling std/*.gala from a downstream Bazel project
// (consumer using local_path_override of @gala) hijacked moduleRoot via
// cwd-walking and tripped GALA-E0011 "type ... redefined".
//
// Shape:
//   - Search path points at the gala module's staged dir (e.g.,
//     execroot/external/gala+/std/), which has gala.mod for the GALA module.
//   - cwd is the consumer's execroot (e.g., execroot/_main/), which is
//     incidentally inside a directory tree containing the consumer's own
//     gala.mod for an unrelated module.
//
// The resolver MUST pick the search path's gala.mod (the actual module being
// transpiled), not the cwd's gala.mod (the consumer that just happens to be
// staged in the parent tree). Otherwise the std files end up registered with
// two different DefinedIn strings (one through the --search resolution path,
// one through cwd-walking) and the duplicate-type check in the analyzer
// trips.
func TestResolver_PrefersSearchPathGalaModOverCwdGalaMod(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "resolver_search_over_cwd_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// The GALA module being transpiled — has gala.mod plus a std-like package
	// dir with a .gala source. Mirrors execroot/external/gala+/.
	galaModuleDir := filepath.Join(tempDir, "gala_module")
	require.NoError(t, os.MkdirAll(galaModuleDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(galaModuleDir, "gala.mod"),
		[]byte("module martianoff/gala\n\ngala dev\n"), 0644))
	stdDir := filepath.Join(galaModuleDir, "std")
	require.NoError(t, os.MkdirAll(stdDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stdDir, "option.gala"),
		[]byte("package std\n"), 0644))

	// Consumer's execroot — has its own gala.mod for an unrelated module.
	// Mirrors what bazel stages at execroot/_main/ when the consumer module
	// has a workspace-root gala.mod and uses local_path_override of @gala.
	consumerExecroot := filepath.Join(tempDir, "consumer_execroot")
	require.NoError(t, os.MkdirAll(consumerExecroot, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(consumerExecroot, "gala.mod"),
		[]byte("module github.com/example/consumer\n\ngala dev\n"), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	require.NoError(t, os.Chdir(consumerExecroot))

	// Bootstrap-style invocation: --search points at the gala module's staged
	// dir, cwd is the consumer's execroot. With the cwd-first lookup the
	// resolver would pick consumerExecroot/gala.mod ("github.com/example/consumer")
	// — wrong; the std files belong to martianoff/gala. With the fix the
	// search path's gala.mod ("martianoff/gala") wins.
	resolver := NewResolver([]string{galaModuleDir})

	require.True(t, resolver.HasGalaMod(),
		"resolver must load the gala module's gala.mod (reached via search path), not the consumer's stale one at cwd")
	assert.Equal(t, "martianoff/gala", resolver.ModuleName(),
		"moduleName must come from the search path's gala.mod (martianoff/gala), not cwd's gala.mod (github.com/example/consumer)")
	assert.Equal(t, galaModuleDir, resolver.ModuleRoot(),
		"moduleRoot must be the search path's gala.mod directory, not the consumer's execroot")
}
