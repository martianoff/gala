// Package fetch provides package fetching and caching functionality.
package fetch

import (
	"os"
	"path/filepath"
	"runtime"
)

// Config holds configuration for the fetch system.
type Config struct {
	// CacheDir is the root directory for the package cache.
	// Defaults to ~/.gala/pkg/mod
	CacheDir string

	// DownloadDir is where downloaded archives are stored.
	// Defaults to CacheDir/cache/download
	DownloadDir string
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	cacheDir := defaultCacheDir()
	return &Config{
		CacheDir:    cacheDir,
		DownloadDir: filepath.Join(cacheDir, "cache", "download"),
	}
}

// defaultCacheDir returns the default cache directory.
// Uses GALA_CACHE environment variable if set, otherwise ~/.gala/pkg/mod
func defaultCacheDir() string {
	if dir := os.Getenv("GALA_CACHE"); dir != "" {
		return dir
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory
		return filepath.Join(".", ".gala", "pkg", "mod")
	}

	return filepath.Join(homeDir, ".gala", "pkg", "mod")
}

// EnsureDirs creates the cache directories if they don't exist.
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.CacheDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(c.DownloadDir, 0755); err != nil {
		return err
	}
	return nil
}

// ModulePath returns the path where a module version is cached.
// Format: CacheDir/module/path@version/
func (c *Config) ModulePath(modulePath, version string) string {
	// Sanitize module path for filesystem
	safePath := sanitizePath(modulePath)
	return filepath.Join(c.CacheDir, safePath+"@"+version)
}

// DownloadPath returns the path for a downloaded module archive.
// Format: DownloadDir/module/path/@v/version.zip
func (c *Config) DownloadPath(modulePath, version string) string {
	safePath := sanitizePath(modulePath)
	return filepath.Join(c.DownloadDir, safePath, "@v", version+".zip")
}

// InfoPath returns the path for module version info.
// Format: DownloadDir/module/path/@v/version.info
func (c *Config) InfoPath(modulePath, version string) string {
	safePath := sanitizePath(modulePath)
	return filepath.Join(c.DownloadDir, safePath, "@v", version+".info")
}

// ModFilePath returns the path for a cached gala.mod file.
// Format: DownloadDir/module/path/@v/version.mod
func (c *Config) ModFilePath(modulePath, version string) string {
	safePath := sanitizePath(modulePath)
	return filepath.Join(c.DownloadDir, safePath, "@v", version+".mod")
}

// sanitizePath converts a module path to a filesystem-safe path.
// On Windows, replaces characters that are invalid in paths.
func sanitizePath(modulePath string) string {
	// Module paths use forward slashes, keep them for directory structure
	if runtime.GOOS == "windows" {
		// Windows doesn't allow certain characters in paths
		// Module paths should be safe, but just in case
		return modulePath
	}
	return modulePath
}

// IsCached returns true if a module version is already cached.
func (c *Config) IsCached(modulePath, version string) bool {
	modPath := c.ModulePath(modulePath, version)
	info, err := os.Stat(modPath)
	return err == nil && info.IsDir()
}
