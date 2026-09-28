package fetch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/sum"
)

// buildArtifacts are the files a gala build used to write into a cached
// module: the transpiled package beside each .gala source, and the analysis
// cache.
var buildArtifacts = map[string]string{
	"lib.gen.go":                "package lib\n",
	"sub/sub.gen.go":            "package sub\n",
	".gala/cache/v1/entry.json": "{}",
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

// A cached module's hash covers the files it was stored with, and nothing a
// build wrote into its directory afterwards.
func TestCache_HashCoversStoredFilesOnly(t *testing.T) {
	cache, sourceDir := newStoreFixture(t)
	require.NoError(t, cache.Store("github.com/test/lib", "v1.0.0", sourceDir))
	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")

	stored, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)
	clean, err := sum.HashDir(sourceDir)
	require.NoError(t, err)
	assert.Equal(t, clean, stored, "the hash of a stored module is the hash of what was fetched")

	writeFiles(t, modPath, buildArtifacts)
	after, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, stored, after, "files a build wrote into the cache changed the hash")
	require.NoError(t, cache.Verify("github.com/test/lib", "v1.0.0", stored))

	// A file the module was stored with still counts.
	require.NoError(t, os.WriteFile(filepath.Join(modPath, "lib.gala"), []byte("package lib // edited\n"), 0o644))
	edited, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)
	assert.NotEqual(t, stored, edited, "an edit to a stored file must change the hash")
	require.Error(t, cache.Verify("github.com/test/lib", "v1.0.0", stored))
}

// A file added to a cached module that a build would read is not covered by
// the hash, so verification rejects it rather than vouching for it.
func TestCache_VerifyRejectsAddedSource(t *testing.T) {
	cache, sourceDir := newStoreFixture(t)
	require.NoError(t, cache.Store("github.com/test/lib", "v1.0.0", sourceDir))
	stored, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)

	writeFiles(t, cache.Config().ModulePath("github.com/test/lib", "v1.0.0"), map[string]string{"extra.gala": "package lib\n"})
	err = cache.Verify("github.com/test/lib", "v1.0.0", stored)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "extra.gala")
}

// A module stored before the completion marker listed its files, and then
// written into by a build, still hashes as a clean fetch does: what builds
// wrote there is left out.
func TestCache_LegacyMarkerIgnoresBuildArtifacts(t *testing.T) {
	cache, sourceDir := newStoreFixture(t)
	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")
	require.NoError(t, cache.Store("github.com/test/lib", "v1.0.0", sourceDir))
	// As an older gala left it: an empty marker, and the build's files.
	require.NoError(t, os.WriteFile(filepath.Join(modPath, completeMarkerName), nil, 0o644))
	writeFiles(t, modPath, buildArtifacts)
	require.True(t, cache.Config().IsCached("github.com/test/lib", "v1.0.0"), "a legacy tree stays cached")

	clean, err := sum.HashDir(sourceDir)
	require.NoError(t, err)
	got, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, clean, got)
	require.NoError(t, cache.Verify("github.com/test/lib", "v1.0.0", clean))
}
