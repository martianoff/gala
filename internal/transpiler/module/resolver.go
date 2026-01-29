// Package module provides module root discovery and package path resolution.
package module

import (
	"os"
	"path/filepath"
	"strings"

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
	moduleRoot  string    // Filesystem path to module root (where go.mod is located)
	moduleName  string    // Module name from go.mod (e.g., "martianoff/gala")
	searchPaths []string  // Fallback search paths when module resolution fails
	galaMod     *mod.File // Parsed gala.mod file (if present)
	galaModPath string    // Path to gala.mod file
}

// NewResolver creates a Resolver by searching for go.mod and gala.mod.
// It first tries the current working directory, then falls back to searchPaths.
//
// The resolver will:
// 1. Walk up from cwd looking for go.mod
// 2. If not found, try each search path
// 3. Extract module name from go.mod when found
// 4. Load gala.mod if present (for replace directives)
func NewResolver(searchPaths []string) *Resolver {
	moduleRoot, moduleName := findModuleRootFromCwdOrPaths(searchPaths)
	r := &Resolver{
		moduleRoot:  moduleRoot,
		moduleName:  moduleName,
		searchPaths: searchPaths,
	}

	// Try to load gala.mod if module root was found
	if moduleRoot != "" {
		r.loadGalaMod(moduleRoot)
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
// 3. Fall back to search paths
//
// Examples:
//   - "martianoff/gala/std" with moduleName "martianoff/gala" -> "{moduleRoot}/std"
//   - "std" with moduleRoot set -> "{moduleRoot}/std"
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

	// Strategy 3: Search paths fallback
	for _, sp := range r.searchPaths {
		dirPath := filepath.Join(sp, importPath)
		if isValidPackageDir(dirPath) {
			return dirPath, nil
		}
	}

	return "", &PackageNotFoundError{ImportPath: importPath}
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
