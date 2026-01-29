// Package module provides module root discovery and package path resolution.
package module

import (
	"os"
	"path/filepath"
	"strings"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
)

// Resolver handles module root discovery and package path resolution.
// It finds go.mod by walking up the directory tree and uses the module
// name to resolve relative package paths.
//
// Example usage:
//
//	resolver := NewResolver(searchPaths)
//	fsPath, err := resolver.ResolvePackagePath("martianoff/gala/std")
type Resolver struct {
	moduleRoot  string       // Filesystem path to module root (where go.mod is located)
	moduleName  string       // Module name from go.mod (e.g., "martianoff/gala")
	searchPaths []string     // Fallback search paths when module resolution fails
	galaMod     *mod.File    // Parsed gala.mod file (if present)
	galaModPath string       // Path to gala.mod file
	cache       *fetch.Cache // GALA dependency cache
}

// NewResolver creates a Resolver by searching for go.mod and gala.mod.
// It first tries the current working directory, then falls back to searchPaths.
//
// The resolver will:
// 1. Walk up from cwd looking for go.mod or gala.mod
// 2. If not found, try each search path
// 3. Extract module name from go.mod or gala.mod when found
// 4. Load gala.mod if present (for replace directives and dependencies)
// 5. Initialize the GALA dependency cache
func NewResolver(searchPaths []string) *Resolver {
	moduleRoot, moduleName := findModuleRootFromCwdOrPaths(searchPaths)

	r := &Resolver{
		moduleRoot:  moduleRoot,
		moduleName:  moduleName,
		searchPaths: searchPaths,
		cache:       fetch.NewCache(fetch.DefaultConfig()),
	}

	// Try to load gala.mod if module root was found
	if moduleRoot != "" {
		r.loadGalaMod(moduleRoot)
	} else {
		// If no go.mod found, try to find gala.mod directly
		galaModRoot := findGalaModRoot(searchPaths)
		if galaModRoot != "" {
			r.moduleRoot = galaModRoot
			r.loadGalaMod(galaModRoot)
			// If gala.mod was loaded, use its module name
			if r.galaMod != nil {
				r.moduleName = r.galaMod.Module.Path
			}
		}
	}

	return r
}

// loadGalaMod attempts to load gala.mod from the given directory.
func (r *Resolver) loadGalaMod(dir string) {
	galaModPath := filepath.Join(dir, "gala.mod")
	galaMod, err := mod.ParseFile(galaModPath)
	if err == nil {
		r.galaMod = galaMod
		r.galaModPath = galaModPath
	}
}

// GalaMod returns the parsed gala.mod file, or nil if not present.
func (r *Resolver) GalaMod() *mod.File {
	return r.galaMod
}

// HasGalaMod returns true if a gala.mod file was found.
func (r *Resolver) HasGalaMod() bool {
	return r.galaMod != nil
}

// ModuleRoot returns the filesystem path to the module root directory.
// Returns empty string if no go.mod was found.
func (r *Resolver) ModuleRoot() string {
	return r.moduleRoot
}

// ModuleName returns the module name from go.mod (e.g., "martianoff/gala").
// Returns empty string if no go.mod was found.
func (r *Resolver) ModuleName() string {
	return r.moduleName
}

// ResolvePackagePath converts an import path to a filesystem path.
//
// Resolution strategy:
// 0. Check replace directives in gala.mod
// 1. If import path starts with module name, resolve relative to module root
// 2. If import path is a simple name (no slashes), try as subdir of module root
// 3. Check gala.mod require directives and resolve from cache
// 4. Fall back to search paths
//
// Examples:
//   - "martianoff/gala/std" with moduleName "martianoff/gala" -> "{moduleRoot}/std"
//   - "std" with moduleRoot set -> "{moduleRoot}/std"
//   - "github.com/user/pkg" in require -> cache path
//   - "external/pkg" -> tries each search path
func (r *Resolver) ResolvePackagePath(importPath string) (string, error) {
	// Strategy 0: Check replace directives in gala.mod
	if r.galaMod != nil {
		if replaced := r.applyReplace(importPath); replaced != "" {
			if isValidPackageDir(replaced) {
				return replaced, nil
			}
		}
	}

	// Strategy 1: Module-relative resolution
	if r.moduleRoot != "" && r.moduleName != "" {
		if strings.HasPrefix(importPath, r.moduleName+"/") {
			// Full module path: "martianoff/gala/std" -> "{moduleRoot}/std"
			relPath := strings.TrimPrefix(importPath, r.moduleName+"/")
			dirPath := filepath.Join(r.moduleRoot, relPath)
			if isValidPackageDir(dirPath) {
				return dirPath, nil
			}
		}
	}

	// Strategy 2: Simple package name (e.g., "std")
	if r.moduleRoot != "" && !strings.Contains(importPath, "/") {
		dirPath := filepath.Join(r.moduleRoot, importPath)
		if isValidPackageDir(dirPath) {
			return dirPath, nil
		}
	}

	// Strategy 3: Check gala.mod require directives and resolve from cache
	if r.galaMod != nil && r.cache != nil {
		if cachePath, err := r.resolveFromCache(importPath); err == nil {
			return cachePath, nil
		}
	}

	// Strategy 4: Search paths fallback
	for _, sp := range r.searchPaths {
		dirPath := filepath.Join(sp, importPath)
		if isValidPackageDir(dirPath) {
			return dirPath, nil
		}
	}

	return "", &PackageNotFoundError{ImportPath: importPath}
}

// resolveFromCache checks if the import path is in gala.mod require list
// and resolves it from the dependency cache.
func (r *Resolver) resolveFromCache(importPath string) (string, error) {
	if r.galaMod == nil || r.cache == nil {
		return "", &PackageNotFoundError{ImportPath: importPath}
	}

	// Check if this import path matches any require directive
	for _, req := range r.galaMod.Require {
		if req.Path == importPath {
			// Found in require list, resolve from cache
			return r.cache.ResolveVersion(req.Path, req.Version)
		}
		// Also check if it's a subpackage of a required module
		if strings.HasPrefix(importPath, req.Path+"/") {
			// It's a subpackage like "github.com/user/mod/subpkg"
			basePath, err := r.cache.ResolveVersion(req.Path, req.Version)
			if err != nil {
				continue
			}
			subPath := strings.TrimPrefix(importPath, req.Path+"/")
			fullPath := filepath.Join(basePath, subPath)
			if isValidPackageDir(fullPath) {
				return fullPath, nil
			}
		}
	}

	return "", &PackageNotFoundError{ImportPath: importPath}
}

// IsGalaPackage checks if the import path refers to a GALA package
// (i.e., it's in gala.mod require list and has .gala files in the cache).
func (r *Resolver) IsGalaPackage(importPath string) bool {
	// Check if it's the current module
	if r.moduleName != "" && strings.HasPrefix(importPath, r.moduleName+"/") {
		return true
	}

	// Check gala.mod require list
	if r.galaMod != nil {
		for _, req := range r.galaMod.Require {
			if req.Path == importPath || strings.HasPrefix(importPath, req.Path+"/") {
				// If explicitly marked as Go in gala.mod, it's not a GALA package
				if req.Go {
					return false
				}
				// Found in require list, now check if it's actually a GALA package
				// by looking for .gala files or gala.mod in the cache
				return r.isGalaPackageInCache(req.Path, req.Version)
			}
		}
	}

	return false
}

// isGalaPackageInCache checks if a cached module is a GALA package
// (has .gala files or gala.mod).
func (r *Resolver) isGalaPackageInCache(modulePath, version string) bool {
	if r.cache == nil {
		return false
	}

	// Check if module is cached
	modPath, err := r.cache.ResolveVersion(modulePath, version)
	if err != nil {
		return false
	}

	// Check for gala.mod
	galaModPath := filepath.Join(modPath, "gala.mod")
	if _, err := os.Stat(galaModPath); err == nil {
		return true
	}

	// Check for .gala files
	entries, err := os.ReadDir(modPath)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".gala") {
			return true
		}
	}

	return false
}

// IsGoPackage checks if an import path refers to a Go package
// (in require list but not a GALA package).
func (r *Resolver) IsGoPackage(importPath string) bool {
	if r.galaMod == nil {
		return false
	}

	for _, req := range r.galaMod.Require {
		if req.Path == importPath || strings.HasPrefix(importPath, req.Path+"/") {
			// If explicitly marked as Go in gala.mod, trust that
			if req.Go {
				return true
			}
			// Otherwise check if it's NOT a GALA package by scanning files
			return !r.isGalaPackageInCache(req.Path, req.Version)
		}
	}

	return false
}

// GetRequiredVersion returns the version of a required dependency, or empty if not found.
func (r *Resolver) GetRequiredVersion(modulePath string) string {
	if r.galaMod == nil {
		return ""
	}
	for _, req := range r.galaMod.Require {
		if req.Path == modulePath {
			return req.Version
		}
	}
	return ""
}

// Cache returns the dependency cache.
func (r *Resolver) Cache() *fetch.Cache {
	return r.cache
}

// applyReplace checks if the import path matches any replace directive
// and returns the replacement path. Returns empty string if no match.
func (r *Resolver) applyReplace(importPath string) string {
	if r.galaMod == nil {
		return ""
	}

	for _, rep := range r.galaMod.Replace {
		// Check for exact match or prefix match
		if rep.Old.Path == importPath ||
			(rep.Old.Version == "" && strings.HasPrefix(importPath, rep.Old.Path+"/")) {

			newPath := rep.New.Path

			// Handle prefix replacement
			if strings.HasPrefix(importPath, rep.Old.Path+"/") {
				suffix := strings.TrimPrefix(importPath, rep.Old.Path)
				newPath = rep.New.Path + suffix
			}

			// Handle local paths (relative to gala.mod location)
			if rep.New.IsLocal() {
				galaModDir := filepath.Dir(r.galaModPath)
				newPath = filepath.Join(galaModDir, newPath)
			}

			return newPath
		}
	}

	return ""
}

// PackageNotFoundError is returned when a package cannot be resolved.
type PackageNotFoundError struct {
	ImportPath string
}

func (e *PackageNotFoundError) Error() string {
	return "package not found: " + e.ImportPath
}

// findGalaModRoot searches for gala.mod starting from cwd, then falling back to search paths.
// Returns the directory containing gala.mod, or empty string if not found.
func findGalaModRoot(searchPaths []string) string {
	// Try current working directory first
	cwd, _ := os.Getwd()
	if root := findGalaModFromDir(cwd); root != "" {
		return root
	}

	// Fall back to search paths
	for _, sp := range searchPaths {
		absPath, err := filepath.Abs(sp)
		if err != nil {
			continue
		}
		if root := findGalaModFromDir(absPath); root != "" {
			return root
		}
	}

	return ""
}

// findGalaModFromDir walks up from startPath looking for gala.mod.
func findGalaModFromDir(startPath string) string {
	dir := startPath

	// If startPath is a file, use its directory
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	// Walk up looking for gala.mod
	for {
		galaModPath := filepath.Join(dir, "gala.mod")
		if _, err := os.Stat(galaModPath); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			break
		}
		dir = parent
	}

	return ""
}

// findModuleRootFromCwdOrPaths searches for go.mod starting from cwd,
// then falling back to search paths.
func findModuleRootFromCwdOrPaths(searchPaths []string) (moduleRoot, moduleName string) {
	// Try current working directory first
	cwd, _ := os.Getwd()
	moduleRoot, moduleName = FindModuleRoot(cwd)
	if moduleRoot != "" {
		return moduleRoot, moduleName
	}

	// Fall back to search paths
	for _, sp := range searchPaths {
		absPath, err := filepath.Abs(sp)
		if err != nil {
			continue
		}
		moduleRoot, moduleName = FindModuleRoot(absPath)
		if moduleRoot != "" {
			return moduleRoot, moduleName
		}
	}

	return "", ""
}

// FindModuleRoot walks up from startPath looking for go.mod.
// Returns the module root path and module name, or empty strings if not found.
//
// This is exported for use cases that need direct module root discovery
// without creating a full Resolver.
func FindModuleRoot(startPath string) (moduleRoot, moduleName string) {
	dir := startPath

	// If startPath is a file, use its directory
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	// Walk up looking for go.mod
	for {
		modPath := filepath.Join(dir, "go.mod")
		content, err := os.ReadFile(modPath)
		if err == nil {
			// Parse module name from go.mod
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					moduleName = strings.TrimSpace(strings.TrimPrefix(line, "module "))
					return dir, moduleName
				}
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root, no go.mod found
			break
		}
		dir = parent
	}

	return "", ""
}

// isValidPackageDir checks if a directory exists and could contain a package.
func isValidPackageDir(dirPath string) bool {
	info, err := os.Stat(dirPath)
	return err == nil && info.IsDir()
}
