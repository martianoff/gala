package build

import (
	"os"
	"path/filepath"
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
