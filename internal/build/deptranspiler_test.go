package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyNonGalaFiles(t *testing.T) {
	// Create source directory structure simulating a GALA module with Go subpackages
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create source files
	// Root .gala files (should be skipped - already transpiled)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "server.gala"), []byte("package server"), 0644))
	// Root .go file (should be copied)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "helpers.go"), []byte("package server"), 0644))
	// gala.mod (should be skipped)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "gala.mod"), []byte("module test"), 0644))
	// go.mod (should be skipped - we generate our own)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module test"), 0644))

	// Go subpackage directory
	httpcore := filepath.Join(srcDir, "httpcore")
	require.NoError(t, os.MkdirAll(httpcore, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(httpcore, "httpcore.go"), []byte("package httpcore\n\nfunc Handler() {}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(httpcore, "types.go"), []byte("package httpcore\n\ntype Request struct{}"), 0644))

	// Nested Go subpackage
	middleware := filepath.Join(srcDir, "httpcore", "middleware")
	require.NoError(t, os.MkdirAll(middleware, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(middleware, "auth.go"), []byte("package middleware"), 0644))

	// Hidden directory (should be skipped)
	gitDir := filepath.Join(srcDir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0644))

	// Pre-existing .gen.go in dst (should NOT be overwritten)
	require.NoError(t, os.WriteFile(filepath.Join(dstDir, "helpers.go"), []byte("GENERATED"), 0644))

	// Run copyNonGalaFiles
	err := copyNonGalaFiles(srcDir, dstDir, false)
	require.NoError(t, err)

	// Verify .gala files were NOT copied
	assert.NoFileExists(t, filepath.Join(dstDir, "server.gala"))

	// Verify gala.mod was NOT copied
	assert.NoFileExists(t, filepath.Join(dstDir, "gala.mod"))

	// Verify go.mod was NOT copied
	content, err := os.ReadFile(filepath.Join(dstDir, "go.mod"))
	assert.True(t, os.IsNotExist(err) || (err == nil && string(content) != "module test"),
		"go.mod from source should not overwrite destination")

	// Verify hidden dirs were NOT copied
	assert.NoDirExists(t, filepath.Join(dstDir, ".git"))

	// Verify Go subpackage files WERE copied
	data, err := os.ReadFile(filepath.Join(dstDir, "httpcore", "httpcore.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "package httpcore")

	data, err = os.ReadFile(filepath.Join(dstDir, "httpcore", "types.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "type Request struct{}")

	// Verify nested subpackage was copied
	data, err = os.ReadFile(filepath.Join(dstDir, "httpcore", "middleware", "auth.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "package middleware")

	// Verify pre-existing file was NOT overwritten
	data, err = os.ReadFile(filepath.Join(dstDir, "helpers.go"))
	require.NoError(t, err)
	assert.Equal(t, "GENERATED", string(data))
}

func TestCopyNonGalaFiles_SkipsSymlinks(t *testing.T) {
	// Bazel creates symlinks (Linux) or junctions (Windows) that may point to
	// nonexistent targets. copyNonGalaFiles should skip all symlinks and still
	// copy regular files.
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Valid Go subpackage
	sub := filepath.Join(srcDir, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "pkg.go"), []byte("package pkg"), 0644))

	// Create a broken symlink (simulates bazel-bin junction)
	brokenLink := filepath.Join(srcDir, "bazel-broken")
	_ = os.Symlink(filepath.Join(srcDir, "nonexistent-target"), brokenLink)

	// Create a symlink to a valid file (should also be skipped — we only copy regular files)
	validTarget := filepath.Join(srcDir, "pkg", "pkg.go")
	validLink := filepath.Join(srcDir, "link-to-file")
	_ = os.Symlink(validTarget, validLink)

	err := copyNonGalaFiles(srcDir, dstDir, false)
	require.NoError(t, err, "copyNonGalaFiles should not fail on symlinks")

	// Valid file should still be copied
	data, err := os.ReadFile(filepath.Join(dstDir, "pkg", "pkg.go"))
	require.NoError(t, err)
	assert.Equal(t, "package pkg", string(data))
}

func TestRewritePackageToMain_ImportBlock(t *testing.T) {
	input := `package server

import (
	"fmt"
	"martianoff/gala/std"
)

func TestFoo() {
	fmt.Println("hello")
}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `. "gala-build-workspace/gen"`)
	assert.Contains(t, result, `"fmt"`)
	assert.Contains(t, result, `"martianoff/gala/std"`)
}

func TestRewritePackageToMain_SingleImport(t *testing.T) {
	input := `package server

import "fmt"

func main() {}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `. "gala-build-workspace/gen"`)
	assert.Contains(t, result, `"fmt"`)
}

func TestRewritePackageToMain_NoImport(t *testing.T) {
	input := `package server

func main() {}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `import . "gala-build-workspace/gen"`)
}

func TestRewritePackageToMain_DotImport(t *testing.T) {
	input := `package server

import . "martianoff/gala/std"

func main() {}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `. "gala-build-workspace/gen"`)
	assert.Contains(t, result, `. "martianoff/gala/std"`)
}

func TestRewriteTestFilesAsMain(t *testing.T) {
	dir := t.TempDir()

	// Write a test .gen.go file
	testCode := `package server

import "martianoff/gala/std"

func TestSomething() {
	_ = std.NewImmutable(42)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server_test.gen.go"), []byte(testCode), 0644))

	// Write a non-.gen.go file (should not be touched)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package server"), 0644))

	err := rewriteTestFilesAsMain(dir)
	require.NoError(t, err)

	// Verify test file was rewritten
	data, err := os.ReadFile(filepath.Join(dir, "server_test.gen.go"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "package main\n"))
	assert.Contains(t, string(data), `. "gala-build-workspace/gen"`)

	// Verify non-.gen.go was NOT touched
	data, err = os.ReadFile(filepath.Join(dir, "helper.go"))
	require.NoError(t, err)
	assert.Equal(t, "package server", string(data))
}
