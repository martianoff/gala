package fetch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// Create cached module
	modPath := config.ModulePath("github.com/example/utils", "v1.0.0")
	err = os.MkdirAll(modPath, 0755)
	require.NoError(t, err)

	// Now it should be cached
	assert.True(t, config.IsCached("github.com/example/utils", "v1.0.0"))
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
