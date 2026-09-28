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

// DefaultConfig returns the default configuration: the module cache at
// ModuleCacheDir(DefaultGalaHome()).
func DefaultConfig() *Config {
	return NewConfig(ModuleCacheDir(DefaultGalaHome()))
}

// NewConfig returns the configuration for a module cache rooted at cacheDir.
func NewConfig(cacheDir string) *Config {
	return &Config{
		CacheDir:    cacheDir,
		DownloadDir: filepath.Join(cacheDir, "cache", "download"),
	}
}

// DefaultGalaHome returns the GALA home directory: GALA_HOME if set,
// otherwise ~/.gala.
func DefaultGalaHome() string {
	if dir := os.Getenv("GALA_HOME"); dir != "" {
		return dir
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory
		return filepath.Join(".", ".gala")
	}
	return filepath.Join(homeDir, ".gala")
}

// ModuleCacheDir returns where fetched GALA modules live for a GALA home:
// GALA_CACHE if set, otherwise <galaHome>/pkg/mod.
//
// This is the one place the location is decided. The fetcher writes modules
// there and the builder reads them from there; when each resolved it on its
// own, GALA_HOME moved only the builder's copy and GALA_CACHE only the
// fetcher's, so a fetched dependency landed where the build never looked.
func ModuleCacheDir(galaHome string) string {
	if dir := os.Getenv("GALA_CACHE"); dir != "" {
		return dir
	}
	return filepath.Join(galaHome, "pkg", "mod")
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

// IsCached returns true if a module version is cached completely — see
// IsCompleteModuleDir.
func (c *Config) IsCached(modulePath, version string) bool {
	return IsCompleteModuleDir(c.ModulePath(modulePath, version))
}

// completeMarkerName is written into a module's staged tree last, before the
// tree is renamed into the cache. Its ".gala-" prefix marks it as bookkeeping
// rather than module content (sum.IsModuleContent), so the module hash does
// not cover it.
//
// The name carries the layout of the stored tree. "-v2" trees hold the whole
// module, not only its sources; a tree published under an earlier marker lacks
// the module's data files, so it no longer counts as complete and is fetched
// again once.
//
// The marker's content lists the files the module was stored with, one
// slash-separated path per line, and the module hash covers exactly those (see
// moduleFiles). A marker written before the list existed is empty; the name is
// unchanged so that such a tree, and gala versions that only check for the
// marker, keep working without a refetch.
const completeMarkerName = ".gala-module-complete-v2"

// IsCompleteModuleDir reports whether dir holds a module version a fetch
// finished publishing. The directory existing is not enough: a fetch used to
// create it first and fill it one file at a time, so an interrupted or
// concurrent fetch left a partial module behind that every later build trusted.
// A published tree always carries the marker, because it was written into the
// staged tree before the rename that made the tree visible.
func IsCompleteModuleDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, completeMarkerName))
	return err == nil && !info.IsDir()
}
