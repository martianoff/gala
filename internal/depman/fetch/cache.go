package fetch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
//
// The module is assembled in a private staging directory next to its final
// location and published with a single rename, with the completion marker
// already inside (see IsCompleteModuleDir). Copying straight into the final
// directory made the module visible — and, since presence was all the cache
// checked, trusted — from its first file on: a fetch that was interrupted, or
// that another process read while it was still copying, left a partial module
// that was never fetched again.
//
// Two processes storing the same version each stage their own copy; the first
// rename wins and the other discards its copy.
func (c *Cache) Store(modulePath, ver, sourceDir string) error {
	destDir := c.config.ModulePath(modulePath, ver)

	if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	sweepAbandonedSiblings(destDir)
	// A dot-prefixed sibling: on the same filesystem, so the rename is atomic,
	// and invisible to ListVersions' "<module>@*" glob and to source walks.
	staging, err := os.MkdirTemp(filepath.Dir(destDir), siblingPrefix(destDir, stagingTag))
	if err != nil {
		return fmt.Errorf("failed to create cache staging directory: %w", err)
	}
	defer os.RemoveAll(staging) // no-op once the rename succeeds

	if err := copyModuleFiles(sourceDir, staging); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, completeMarkerName), nil, 0644); err != nil {
		return fmt.Errorf("failed to mark cached module complete: %w", err)
	}
	return publishModuleDir(staging, destDir)
}

// publishModuleDir moves a fully staged module into place.
func publishModuleDir(staging, destDir string) error {
	if IsCompleteModuleDir(destDir) {
		return nil // another process published this version first
	}
	// A directory without the marker was left by an interrupted fetch (or by a
	// gala that predates the marker). It cannot be trusted, and a directory
	// cannot be renamed over it, so it is first moved aside with one rename.
	// Deleting it in place instead could delete a complete copy that another
	// process published between the check above and the delete; a rename
	// moves exactly one tree, and the one moved is checked afterwards.
	//
	// The staging name is already unique, so the set-aside name derived from it
	// is too.
	aside := strings.Replace(staging, siblingPrefix(destDir, stagingTag), siblingPrefix(destDir, staleTag), 1)
	if err := renameWithRetry(destDir, aside); err == nil {
		defer os.RemoveAll(aside)
		if IsCompleteModuleDir(aside) {
			// A concurrent fetch published between the check and the rename:
			// put its copy back and drop ours.
			if err := renameWithRetry(aside, destDir); err == nil {
				return nil
			}
		}
	} else if !os.IsNotExist(err) && !IsCompleteModuleDir(destDir) {
		return fmt.Errorf("failed to set aside incomplete cached module %s: %w", destDir, err)
	}
	if err := renameWithRetry(staging, destDir); err != nil {
		if IsCompleteModuleDir(destDir) {
			return nil // lost the race to a concurrent fetch of the same version
		}
		return fmt.Errorf("failed to publish cached module %s: %w", destDir, err)
	}
	return nil
}

// renameWithRetry renames a directory, retrying briefly when the rename fails
// for a reason that may pass. On Windows, antivirus scanners and indexers open
// files that were just written, and a directory holding an open file cannot be
// renamed until they let go. A rename that fails because the source is gone or
// the destination exists is not retried: another process got there first, and
// the caller decides what that means.
func renameWithRetry(from, to string) error {
	var err error
	delay := 10 * time.Millisecond
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if err = os.Rename(from, to); err == nil || os.IsNotExist(err) {
			return err
		}
		if _, statErr := os.Lstat(to); statErr == nil {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}

// renameAttempts bounds renameWithRetry to about 2.5 seconds of waiting.
const renameAttempts = 8

// The dot-prefixed siblings a Store creates next to a module version: its
// staging tree, and an incomplete tree it moved out of the way.
const (
	stagingTag = "staging"
	staleTag   = "stale"
)

func siblingPrefix(destDir, tag string) string {
	return "." + filepath.Base(destDir) + "." + tag + "-"
}

// abandonedAfter is how old a staging or set-aside sibling must be before a
// later Store deletes it. Store removes its own on every return path; one
// left behind means the process was killed. A live Store finishes in seconds,
// so an hour-old sibling belongs to nobody.
const abandonedAfter = time.Hour

// sweepAbandonedSiblings deletes the staging and set-aside trees that killed
// processes left next to destDir, so they do not accumulate in the cache.
func sweepAbandonedSiblings(destDir string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(destDir), "."+filepath.Base(destDir)+".*"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && time.Since(info.ModTime()) > abandonedAfter {
			os.RemoveAll(m)
		}
	}
}

// copyModuleFiles copies the files a cached module keeps from sourceDir into
// destDir.
func copyModuleFiles(sourceDir, destDir string) error {
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

		// Copy .gala files, .go files, gala.mod, go.sum, and BUILD.bazel
		// .go files are needed for pure Go subpackages within GALA modules
		ext := filepath.Ext(path)
		name := info.Name()
		if ext != ".gala" && ext != ".go" && name != "gala.mod" && name != "go.sum" && name != "BUILD.bazel" {
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

// GetGalaMod returns the gala.mod for a cached module, if present. A module
// that is not cached completely (see IsCached) has none, so callers that can
// fetch — the dependency graph builder — fetch it again instead of resolving
// against a partial copy.
func (c *Cache) GetGalaMod(modulePath, ver string) (*mod.File, error) {
	if !c.config.IsCached(modulePath, ver) {
		return nil, fmt.Errorf("module not cached: %s@%s", modulePath, ver)
	}
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
