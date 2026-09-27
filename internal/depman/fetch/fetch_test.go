package fetch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/sum"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.NotEmpty(t, config.CacheDir)
	assert.NotEmpty(t, config.DownloadDir)
	assert.Contains(t, config.CacheDir, ".gala")
	assert.Contains(t, config.DownloadDir, "download")
}

func TestConfig_ModulePath(t *testing.T) {
	config := &Config{
		CacheDir: "/tmp/gala/pkg/mod",
	}

	path := config.ModulePath("github.com/example/utils", "v1.2.3")
	assert.Equal(t, "/tmp/gala/pkg/mod/github.com/example/utils@v1.2.3", filepath.ToSlash(path))
}

func TestConfig_IsCached(t *testing.T) {
	// Create temp cache directory
	tmpDir, err := os.MkdirTemp("", "gala-cache-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := &Config{
		CacheDir:    tmpDir,
		DownloadDir: filepath.Join(tmpDir, "cache", "download"),
	}

	// Not cached initially
	assert.False(t, config.IsCached("github.com/example/utils", "v1.0.0"))

	// A directory alone is what an interrupted fetch leaves behind: not cached.
	modPath := config.ModulePath("github.com/example/utils", "v1.0.0")
	err = os.MkdirAll(modPath, 0755)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(modPath, "lib.gala"), []byte("package utils\n"), 0644))
	assert.False(t, config.IsCached("github.com/example/utils", "v1.0.0"),
		"a module directory without the completion marker must not count as cached")

	// A published module is.
	markComplete(t, modPath)
	assert.True(t, config.IsCached("github.com/example/utils", "v1.0.0"))
}

// markComplete stands in for a finished Store in tests that lay out a cached
// module by hand.
func markComplete(t *testing.T, modPath string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(modPath, completeMarkerName), nil, 0644))
}

func TestCache_Store(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "gala-cache-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	sourceDir, err := os.MkdirTemp("", "gala-source-test")
	require.NoError(t, err)
	defer os.RemoveAll(sourceDir)

	// Create source files
	err = os.WriteFile(filepath.Join(sourceDir, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(sourceDir, "gala.mod"), []byte("module github.com/test/lib\n"), 0644)
	require.NoError(t, err)
	// This file should not be copied
	err = os.WriteFile(filepath.Join(sourceDir, "README.md"), []byte("# Test\n"), 0644)
	require.NoError(t, err)

	config := &Config{
		CacheDir:    tmpDir,
		DownloadDir: filepath.Join(tmpDir, "cache", "download"),
	}
	cache := NewCache(config)

	// Store module
	err = cache.Store("github.com/test/lib", "v1.0.0", sourceDir)
	require.NoError(t, err)

	// Verify cached files
	modPath := config.ModulePath("github.com/test/lib", "v1.0.0")
	assert.FileExists(t, filepath.Join(modPath, "lib.gala"))
	assert.FileExists(t, filepath.Join(modPath, "gala.mod"))
	assert.NoFileExists(t, filepath.Join(modPath, "README.md"))
}

func TestCache_ListVersions(t *testing.T) {
	// Create temp cache directory
	tmpDir, err := os.MkdirTemp("", "gala-cache-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := &Config{
		CacheDir:    tmpDir,
		DownloadDir: filepath.Join(tmpDir, "cache", "download"),
	}
	cache := NewCache(config)

	// Create cached versions
	for _, ver := range []string{"v1.0.0", "v1.1.0", "v2.0.0", "v1.0.1"} {
		modPath := config.ModulePath("github.com/example/utils", ver)
		err := os.MkdirAll(modPath, 0755)
		require.NoError(t, err)
	}

	// List versions
	versions, err := cache.ListVersions("github.com/example/utils")
	require.NoError(t, err)

	// Should be sorted
	assert.Len(t, versions, 4)
	assert.Equal(t, "v1.0.0", versions[0].String())
	assert.Equal(t, "v1.0.1", versions[1].String())
	assert.Equal(t, "v1.1.0", versions[2].String())
	assert.Equal(t, "v2.0.0", versions[3].String())
}

func TestCache_Hash(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "gala-cache-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := &Config{
		CacheDir:    tmpDir,
		DownloadDir: filepath.Join(tmpDir, "cache", "download"),
	}
	cache := NewCache(config)

	// Create cached module with files
	modPath := config.ModulePath("github.com/test/lib", "v1.0.0")
	err = os.MkdirAll(modPath, 0755)
	require.NoError(t, err)

	markComplete(t, modPath)
	err = os.WriteFile(filepath.Join(modPath, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)

	// Compute hash
	hash, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)
	assert.True(t, len(hash) > 3)
	assert.True(t, hash[:3] == "h1:")
}

func TestCache_Remove(t *testing.T) {
	// Create temp cache directory
	tmpDir, err := os.MkdirTemp("", "gala-cache-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := &Config{
		CacheDir:    tmpDir,
		DownloadDir: filepath.Join(tmpDir, "cache", "download"),
	}
	cache := NewCache(config)

	// Create cached module
	modPath := config.ModulePath("github.com/test/lib", "v1.0.0")
	err = os.MkdirAll(modPath, 0755)
	require.NoError(t, err)
	markComplete(t, modPath)
	err = os.WriteFile(filepath.Join(modPath, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)

	assert.True(t, config.IsCached("github.com/test/lib", "v1.0.0"))

	// Remove
	err = cache.Remove("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)

	assert.False(t, config.IsCached("github.com/test/lib", "v1.0.0"))
}

func TestModulePathToGitURL(t *testing.T) {
	tests := []struct {
		modulePath string
		expected   string
	}{
		{
			"github.com/example/utils",
			"https://github.com/example/utils.git",
		},
		{
			"github.com/example/utils/subpkg",
			"https://github.com/example/utils.git",
		},
		{
			"gitlab.com/group/project",
			"https://gitlab.com/group/project.git",
		},
		{
			"bitbucket.org/user/repo",
			"https://bitbucket.org/user/repo.git",
		},
	}

	for _, tt := range tests {
		result := modulePathToGitURL(tt.modulePath)
		assert.Equal(t, tt.expected, result, "modulePath: %s", tt.modulePath)
	}
}

func TestCache_Info(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "gala-cache-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := &Config{
		CacheDir:    tmpDir,
		DownloadDir: filepath.Join(tmpDir, "cache", "download"),
	}
	cache := NewCache(config)

	// Create cached module with files
	modPath := config.ModulePath("github.com/test/lib", "v1.0.0")
	err = os.MkdirAll(modPath, 0755)
	require.NoError(t, err)

	markComplete(t, modPath)
	err = os.WriteFile(filepath.Join(modPath, "lib.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(modPath, "utils.gala"), []byte("package lib\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(modPath, "gala.mod"), []byte("module github.com/test/lib\n"), 0644)
	require.NoError(t, err)

	// Get info
	info, err := cache.Info("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)

	assert.Equal(t, "github.com/test/lib", info.ModulePath)
	assert.Equal(t, "v1.0.0", info.Version)
	assert.True(t, info.HasGalaMod)
	assert.Equal(t, 2, info.FileCount)
}

// newStoreFixture returns a cache rooted in a temp dir and a module source tree.
func newStoreFixture(t *testing.T) (*Cache, string) {
	t.Helper()
	cacheDir := t.TempDir()
	cache := NewCache(&Config{CacheDir: cacheDir, DownloadDir: filepath.Join(cacheDir, "cache", "download")})
	sourceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "gala.mod"), []byte("module github.com/test/lib\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "lib.gala"), []byte("package lib\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(sourceDir, "sub"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "sub", "sub.gala"), []byte("package sub\n"), 0644))
	return cache, sourceDir
}

// assertNoStagingLeft checks that nothing but published modules sits next to
// the module directory.
func assertNoStagingLeft(t *testing.T, modPath string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(modPath))
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), "."), "staging directory left behind: %s", e.Name())
	}
}

// A store that fails part-way must leave nothing a later build would trust.
// Copying straight into the final directory created it first, so any failure
// after that point left a directory that counted as cached forever.
func TestCache_Store_FailureLeavesNothingCached(t *testing.T) {
	cache, _ := newStoreFixture(t)

	err := cache.Store("github.com/test/lib", "v1.0.0", filepath.Join(t.TempDir(), "vanished-clone"))
	require.Error(t, err)

	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")
	assert.False(t, cache.Config().IsCached("github.com/test/lib", "v1.0.0"))
	assert.NoDirExists(t, modPath, "a failed store must not publish a module directory")
	assertNoStagingLeft(t, modPath)
}

// A partial module left by an interrupted fetch is replaced, not merged into
// and not trusted.
func TestCache_Store_ReplacesIncompleteModule(t *testing.T) {
	cache, sourceDir := newStoreFixture(t)
	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")
	require.NoError(t, os.MkdirAll(modPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(modPath, "leftover.gala"), []byte("package lib\n"), 0644))
	require.False(t, cache.Config().IsCached("github.com/test/lib", "v1.0.0"))

	require.NoError(t, cache.Store("github.com/test/lib", "v1.0.0", sourceDir))

	assert.True(t, cache.Config().IsCached("github.com/test/lib", "v1.0.0"))
	assert.FileExists(t, filepath.Join(modPath, "lib.gala"))
	assert.FileExists(t, filepath.Join(modPath, "sub", "sub.gala"))
	assert.NoFileExists(t, filepath.Join(modPath, "leftover.gala"))
	assertNoStagingLeft(t, modPath)
}

// Concurrent stores of one version all succeed, and exactly one complete copy
// is published.
func TestCache_Store_ConcurrentSameVersion(t *testing.T) {
	cache, sourceDir := newStoreFixture(t)

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = cache.Store("github.com/test/lib", "v1.0.0", sourceDir)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")
	assert.True(t, cache.Config().IsCached("github.com/test/lib", "v1.0.0"))
	assert.FileExists(t, filepath.Join(modPath, "lib.gala"))
	assert.FileExists(t, filepath.Join(modPath, "sub", "sub.gala"))
	assertNoStagingLeft(t, modPath)
}

// The completion marker is bookkeeping, not module content: it must not change
// the hash recorded in gala.sum.
func TestCache_Store_MarkerIsNotHashed(t *testing.T) {
	cache, sourceDir := newStoreFixture(t)
	require.NoError(t, cache.Store("github.com/test/lib", "v1.0.0", sourceDir))

	withMarker, err := cache.Hash("github.com/test/lib", "v1.0.0")
	require.NoError(t, err)
	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")
	require.NoError(t, os.Remove(filepath.Join(modPath, completeMarkerName)))
	withoutMarker, err := sum.HashDir(modPath)
	require.NoError(t, err)
	assert.Equal(t, withoutMarker, withMarker)
}

// Staging directories never show up as cached versions.
func TestCache_ListVersions_IgnoresStaging(t *testing.T) {
	cache, _ := newStoreFixture(t)
	modPath := cache.Config().ModulePath("github.com/test/lib", "v1.0.0")
	require.NoError(t, os.MkdirAll(filepath.Dir(modPath), 0755))
	_, err := os.MkdirTemp(filepath.Dir(modPath), "."+filepath.Base(modPath)+".staging-")
	require.NoError(t, err)

	versions, err := cache.ListVersions("github.com/test/lib")
	require.NoError(t, err)
	assert.Empty(t, versions)
}
