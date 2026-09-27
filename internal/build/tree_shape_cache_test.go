package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// One set of sources can be generated two ways: `gala build` puts the library at
// the gen root, `gala build ./cmd/app` additionally synthesizes a consumer main
// under gen/cmd/main. Keyed on file contents alone the two are indistinguishable,
// so the cache key has to carry the shape as well.
func TestSourceHashDistinguishesTreeShape(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lib.gala")
	require.NoError(t, os.WriteFile(file, []byte("package lib\n"), 0644))
	files := []string{file}

	plain := computeSourceHash(files, toolchainWith("dev", "fp"), "", "")
	consumer := computeSourceHash(files, toolchainWith("dev", "fp"), "cmd/app", "")

	require.NotEmpty(t, plain)
	require.NotEqual(t, plain, consumer,
		"the same sources generated into a different tree shape must not share a cache key")

	// Two different consumers are also distinct, and each shape is stable.
	require.NotEqual(t, consumer, computeSourceHash(files, toolchainWith("dev", "fp"), "cmd/other", ""))
	require.Equal(t, consumer, computeSourceHash(files, toolchainWith("dev", "fp"), "cmd/app", ""))
}

// treeShape names the layout, relative to the project root so that building the
// same project from a different absolute path does not change the key.
func TestTreeShapeIsProjectRelative(t *testing.T) {
	config := isolatedConfig(t)
	projectDir := t.TempDir()
	ws, err := NewWorkspace(config, projectDir, ModeBuild)
	require.NoError(t, err)

	b := &Builder{config: config, workspace: ws}
	require.Empty(t, b.treeShape(), "a plain build is the empty shape")

	b.SetSourceDir(projectDir)
	require.Empty(t, b.treeShape(), "sourceDir == project root is still a plain build")

	b.SetSourceDir(filepath.Join(projectDir, "cmd", "app"))
	require.Equal(t, "cmd/app", b.treeShape(), "slash-separated and project-relative")
}

// The regression itself. A subdirectory build replaces gen/ with a different
// tree shape; the key it leaves behind must describe THAT tree, or the next
// plain build matches the previous plain build's key over a non-empty gen/,
// skips transpilation, and compiles the consumer tree left behind.
func TestSubdirBuildDoesNotLeaveAStalePlainKey(t *testing.T) {
	config := isolatedConfig(t)
	projectDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/demo\n\ngala 0.0.0\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "lib.gala"),
		[]byte("package lib\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "cmd", "app"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "cmd", "app", "main.gala"),
		[]byte("package main\n"), 0644))

	ws, err := NewWorkspace(config, projectDir, ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	hashFile := filepath.Join(ws.Dir, sourceStampName)

	// What a plain build leaves behind.
	plainBuilder := &Builder{config: config, workspace: ws, stdlibVersion: "dev"}
	plainKey := plainBuilder.keyForCurrentSources(t)
	writeSourceStamp(hashFile, sourceStamp{Key: plainKey})

	// A subdirectory build replaces gen/ and records its own shape.
	subBuilder := &Builder{config: config, workspace: ws, stdlibVersion: "dev"}
	subBuilder.SetSourceDir(filepath.Join(projectDir, "cmd", "app"))
	subBuilder.recordSourceHash()

	stored, ok := readSourceStamp(hashFile)
	require.True(t, ok)
	require.NotEqual(t, plainKey, stored.Key,
		"a subdirectory build must not leave the plain build's key describing its tree")

	// So the next plain build sees a mismatch and re-transpiles rather than
	// trusting the consumer tree sitting in gen/.
	require.NotEqual(t, stored.Key, plainBuilder.keyForCurrentSources(t),
		"the following plain build must not match the key the subdirectory build left")
}

// keyForCurrentSources mirrors the key transpile() computes, so a test can
// assert on cache behaviour without running a full transpile.
func (b *Builder) keyForCurrentSources(t *testing.T) string {
	t.Helper()
	files, err := findGalaFilesRecursive(b.workspace.ProjectDir)
	require.NoError(t, err)
	// These builders carry no parsed gala.mod, so there is no dependency key.
	key := b.sourceKey(files, nil)
	require.NotEmpty(t, key)
	return key
}
