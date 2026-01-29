package fetch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
	"martianoff/gala/internal/depman/version"
)

// Cache manages the local package cache.
type Cache struct {
	config *Config
}

// NewCache creates a new Cache with the given configuration.
func NewCache(config *Config) *Cache {
	if config == nil {
		config = DefaultConfig()
	}
	return &Cache{config: config}
}

// Config returns the cache configuration.
func (c *Cache) Config() *Config {
	return c.config
}

// Resolve returns the filesystem path for an import path.
// Returns empty string and error if not cached.
func (c *Cache) Resolve(importPath string) (string, error) {
	// List available versions
	versions, err := c.ListVersions(importPath)
	if err != nil || len(versions) == 0 {
		return "", fmt.Errorf("module not cached: %s", importPath)
	}

	// Return the latest version
	latest := versions[len(versions)-1]
	return c.config.ModulePath(importPath, latest.String()), nil
}

// ResolveVersion returns the filesystem path for a specific version.
func (c *Cache) ResolveVersion(importPath, ver string) (string, error) {
	if !c.config.IsCached(importPath, ver) {
		return "", fmt.Errorf("module version not cached: %s@%s", importPath, ver)
	}
	return c.config.ModulePath(importPath, ver), nil
}

// ListVersions returns all cached versions of a module, sorted ascending.
func (c *Cache) ListVersions(modulePath string) ([]version.Version, error) {
	safePath := sanitizePath(modulePath)
	pattern := filepath.Join(c.config.CacheDir, safePath+"@*")

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var versions []version.Version
	for _, match := range matches {
		base := filepath.Base(match)
		// Extract version from path@version
		idx := strings.LastIndex(base, "@")
		if idx < 0 {
			continue
		}
		verStr := base[idx+1:]
		v, err := version.Parse(verStr)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}

	// Sort versions
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].LessThan(versions[j])
	})

	return versions, nil
}

// Store stores a module in the cache from a source directory.
// It copies all .gala files and gala.mod to the cache.
func (c *Cache) Store(modulePath, ver, sourceDir string) error {
	destDir := c.config.ModulePath(modulePath, ver)

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Walk source directory and copy relevant files
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only copy .gala files, gala.mod, and BUILD.bazel
		ext := filepath.Ext(path)
		name := info.Name()
		if ext != ".gala" && name != "gala.mod" && name != "BUILD.bazel" {
			return nil
		}

		// Calculate relative path
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// Create destination path
		destPath := filepath.Join(destDir, relPath)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		// Copy file
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, content, 0644)
	})
}

// Remove removes a module version from the cache.
func (c *Cache) Remove(modulePath, ver string) error {
	destDir := c.config.ModulePath(modulePath, ver)
	return os.RemoveAll(destDir)
}

// Clean removes all cached modules.
func (c *Cache) Clean() error {
	return os.RemoveAll(c.config.CacheDir)
}

// Hash computes the hash for a cached module.
func (c *Cache) Hash(modulePath, ver string) (string, error) {
	modDir := c.config.ModulePath(modulePath, ver)
	if !c.config.IsCached(modulePath, ver) {
		return "", fmt.Errorf("module not cached: %s@%s", modulePath, ver)
	}
	return sum.HashDir(modDir)
}

// Verify verifies a cached module against an expected hash.
func (c *Cache) Verify(modulePath, ver, expectedHash string) error {
	modDir := c.config.ModulePath(modulePath, ver)
	return sum.Verify(modDir, expectedHash)
}

// GetGalaMod returns the gala.mod for a cached module, if present.
func (c *Cache) GetGalaMod(modulePath, ver string) (*mod.File, error) {
	modDir := c.config.ModulePath(modulePath, ver)
	galaModPath := filepath.Join(modDir, "gala.mod")
	return mod.ParseFile(galaModPath)
}

// CacheInfo holds information about a cached module.
type CacheInfo struct {
	ModulePath string
	Version    string
	Path       string
	HasGalaMod bool
	FileCount  int
}

// Info returns information about a cached module.
func (c *Cache) Info(modulePath, ver string) (*CacheInfo, error) {
	modDir := c.config.ModulePath(modulePath, ver)
	if !c.config.IsCached(modulePath, ver) {
		return nil, fmt.Errorf("module not cached: %s@%s", modulePath, ver)
	}

	info := &CacheInfo{
		ModulePath: modulePath,
		Version:    ver,
		Path:       modDir,
	}

	// Check for gala.mod
	galaModPath := filepath.Join(modDir, "gala.mod")
	if _, err := os.Stat(galaModPath); err == nil {
		info.HasGalaMod = true
	}

	// Count .gala files
	filepath.Walk(modDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && filepath.Ext(path) == ".gala" {
			info.FileCount++
		}
		return nil
	})

	return info, nil
}
