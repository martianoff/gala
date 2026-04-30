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
	r := &Resolver{
		searchPaths: searchPaths,
		cache:       fetch.NewCache(fetch.DefaultConfig()),
	}

	// Prefer a gala.mod located in (or above) one of the search paths over an
	// unrelated parent go.mod found by walking up from cwd. The first search
	// path is the project directory; if it (or one of its ancestors stopping
	// at a gala.mod) declares the module, that is the authoritative module
	// for this build. Without this preference a project under e.g.
	// /tmp/qa_consumer that has no go.mod will inherit a stale parent go.mod
	// (e.g. /tmp/go.mod), and require/replace directives in the project's
	// gala.mod will be invisible — so cross-module GALA dependencies are
	// silently demoted to "Go package" and their TypeMetadata is dropped.
	galaModRoot := findGalaModRoot(searchPaths)
	if galaModRoot != "" {
		r.moduleRoot = galaModRoot
		r.loadGalaMod(galaModRoot)
		if r.galaMod != nil && r.galaMod.Module.Path != "" {
			r.moduleName = r.galaMod.Module.Path
		}
		// Fall back to go.mod-derived module name only if gala.mod did not
		// declare one (rare).
		if r.moduleName == "" {
			if _, modName := FindModuleRoot(galaModRoot); modName != "" {
				r.moduleName = modName
			}
		}
		return r
	}

	// No gala.mod anywhere — fall back to go.mod discovery (legacy behaviour).
	moduleRoot, moduleName := findModuleRootFromCwdOrPaths(searchPaths)
	r.moduleRoot = moduleRoot
	r.moduleName = moduleName
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
		if relPath, ok := hasModulePrefix(importPath, r.moduleName); ok {
			// Full module path: "martianoff/gala/std" -> "{moduleRoot}/std"
			dirPath := filepath.Join(r.moduleRoot, relPath)
			if isValidPackageDir(dirPath) {
				return dirPath, nil
			}
		}
		// Also check if importPath matches the module root itself (root package)
		if matchesModuleName(importPath, r.moduleName) {
			if isValidPackageDir(r.moduleRoot) {
				return r.moduleRoot, nil
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

	// Strategy 4: Search paths fallback — try as subdirectory of each search path
	for _, sp := range r.searchPaths {
		dirPath := filepath.Join(sp, importPath)
		if isValidPackageDir(dirPath) {
			return dirPath, nil
		}
	}

	// Strategy 5: Check if any search path is itself a module root whose module name
	// matches the import path. This handles cross-module resolution when multiple
	// GALA modules are provided via --search (e.g., gala + gala-server).
	// Also handles VCS host prefix mismatches (e.g., import "martianoff/gala-server"
	// matching module "github.com/martianoff/gala-server").
	for _, sp := range r.searchPaths {
		spModRoot, spModName := findModuleRootForSearchPath(sp)
		if spModName == "" || spModRoot == r.moduleRoot {
			continue // skip primary module (already handled in Strategy 1)
		}
		if matchesModuleName(importPath, spModName) {
			if isValidPackageDir(spModRoot) {
				return spModRoot, nil
			}
		}
		if relPath, ok := hasModulePrefix(importPath, spModName); ok {
			dirPath := filepath.Join(spModRoot, relPath)
			if isValidPackageDir(dirPath) {
				return dirPath, nil
			}
		}
	}

	// Strategy 6: Recursive directory search — when the import path's directory name
	// doesn't match the filesystem layout (e.g., importpath "martianoff/gala/crossfile"
	// maps to "crossfile" but the actual directory is "examples/.../crossfile/").
	// Search the module root and search paths for a directory whose path matches
	// the longest possible suffix of the import path and contains .gala files.
	//
	// Matching by longest suffix (rather than just the last segment) prevents a
	// distinct package from accidentally claiming the lookup when two unrelated
	// directories happen to share the same base name. For example,
	// `examples/sealed_done_state_pkg/state` and `examples/multipackage_subpkg/state`
	// both have base name `state`; matching only the base name picks whichever
	// directory the walk visits first, surfacing as a confusing
	// "extractor must define an Unapply method" error when the consumer tries
	// to pattern-match a sealed case from the package it actually imported.
	//
	// For module-prefixed import paths, the relative-to-module-root portion is
	// used as the suffix to match (the module name itself is not part of any
	// real on-disk path). Other paths use the full import path as the suffix.
	suffixPath := importPath
	if r.moduleName != "" {
		if rel, ok := hasModulePrefix(importPath, r.moduleName); ok {
			suffixPath = rel
		}
	}
	searchRoots := make([]string, 0, 1+len(r.searchPaths))
	if r.moduleRoot != "" {
		searchRoots = append(searchRoots, r.moduleRoot)
	}
	searchRoots = append(searchRoots, r.searchPaths...)
	for _, root := range searchRoots {
		if found := findPackageDirByLongestSuffix(root, suffixPath); found != "" {
			return found, nil
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

// matchesModuleName checks if importPath matches moduleName, accounting for
// VCS host prefixes. GALA imports use short paths (e.g., "martianoff/gala-server")
// while go.mod may use full paths (e.g., "github.com/martianoff/gala-server").
// Returns true if the paths match after stripping a VCS host prefix from either side.
func matchesModuleName(importPath, moduleName string) bool {
	if importPath == moduleName {
		return true
	}
	// Try stripping VCS host prefix from moduleName
	if stripped := stripVCSHost(moduleName); stripped != "" && stripped == importPath {
		return true
	}
	// Try stripping VCS host prefix from importPath
	if stripped := stripVCSHost(importPath); stripped != "" && stripped == moduleName {
		return true
	}
	return false
}

// hasModulePrefix checks if importPath starts with moduleName+"/", accounting for
// VCS host prefix differences. Returns the relative path after the module prefix,
// or empty string if no match.
func hasModulePrefix(importPath, moduleName string) (relPath string, ok bool) {
	if strings.HasPrefix(importPath, moduleName+"/") {
		return strings.TrimPrefix(importPath, moduleName+"/"), true
	}
	// Try stripping VCS host from moduleName
	if stripped := stripVCSHost(moduleName); stripped != "" {
		if strings.HasPrefix(importPath, stripped+"/") {
			return strings.TrimPrefix(importPath, stripped+"/"), true
		}
	}
	// Try stripping VCS host from importPath
	if stripped := stripVCSHost(importPath); stripped != "" {
		if strings.HasPrefix(stripped, moduleName+"/") {
			return strings.TrimPrefix(stripped, moduleName+"/"), true
		}
	}
	return "", false
}

// ResolveGoImportPath returns the actual Go import path for a GALA import.
// If the GALA import path differs from the Go module path (e.g., "martianoff/gala-server"
// vs "github.com/martianoff/gala-server"), this returns the full Go path.
// Returns empty string if no mapping is needed (paths are the same).
func (r *Resolver) ResolveGoImportPath(importPath string) string {
	// Check search paths for module roots with VCS prefix differences
	for _, sp := range r.searchPaths {
		spModRoot, spModName := findModuleRootForSearchPath(sp)
		if spModName == "" || spModRoot == r.moduleRoot {
			continue
		}
		_ = spModRoot
		// If importPath matches after stripping VCS host from moduleName
		if importPath != spModName && matchesModuleName(importPath, spModName) {
			return spModName // Return the full Go module path
		}
		// Handle subpackage: "martianoff/gala-server/sub" → "github.com/martianoff/gala-server/sub"
		if relPath, ok := hasModulePrefix(importPath, spModName); ok {
			if importPath != spModName+"/"+relPath {
				return spModName + "/" + relPath
			}
		}
	}
	return ""
}

// stripVCSHost removes a known VCS host prefix (github.com/, gitlab.com/, etc.)
// from an import path. Returns empty string if no VCS host prefix is found.
func stripVCSHost(path string) string {
	hosts := []string{"github.com/", "gitlab.com/", "bitbucket.org/", "sr.ht/~"}
	for _, host := range hosts {
		if strings.HasPrefix(path, host) {
			return strings.TrimPrefix(path, host)
		}
	}
	return ""
}

// IsGalaPackage checks if the import path refers to a GALA package
// (i.e., it's in gala.mod require list and has .gala files in the cache).
func (r *Resolver) IsGalaPackage(importPath string) bool {
	// Check if it's the current module (root package or subpackage)
	if r.moduleName != "" && (matchesModuleName(importPath, r.moduleName) || func() bool {
		_, ok := hasModulePrefix(importPath, r.moduleName)
		return ok
	}()) {
		return true
	}

	// Check replace directives first: a require like
	//     require example.com/qalib v0.0.0
	//     replace example.com/qalib => ../qa_lib
	// resolves through the local path, never touching the cache. Without
	// this branch IsGalaPackage falls back to isGalaPackageInCache, which
	// fails because the dependency was never fetched, and the dep is then
	// misclassified as a Go package — its .gala-derived TypeMetadata is
	// then dropped on the consumer side (sealed-case Apply lowering breaks).
	if r.galaMod != nil {
		if replaced := r.applyReplace(importPath); replaced != "" {
			return isGalaDir(replaced)
		}
	}

	// Check gala.mod require list. The cache check is authoritative when
	// the dep is actually cached — but under Bazel (bazel_dep +
	// local_path_override) the dep is materialised in
	// execroot/external/<repo>+/ and the cache at ~/.gala/cache is never
	// populated. In that situation isGalaPackageInCache returns false even
	// though the dep IS a GALA package, just one that reaches the build
	// via a search path instead of through the cache. So fall through to
	// the search-path scan below when the cache check is negative — only
	// treat the require entry as authoritative when the dep is explicitly
	// marked Go (req.Go), which is a deliberate gala.mod declaration that
	// should override search-path discovery.
	if r.galaMod != nil {
		for _, req := range r.galaMod.Require {
			if matchesModuleName(importPath, req.Path) || func() bool {
				_, ok := hasModulePrefix(importPath, req.Path)
				return ok
			}() {
				// If explicitly marked as Go in gala.mod, it's not a GALA package
				if req.Go {
					return false
				}
				if r.isGalaPackageInCache(req.Path, req.Version) {
					return true
				}
				// Cache miss — let the search-path scan decide.
				break
			}
		}
	}

	// Check if any search path is a module root whose name matches
	for _, sp := range r.searchPaths {
		spModRoot, spModName := findModuleRootForSearchPath(sp)
		if spModName == "" || spModName == r.moduleName {
			continue
		}
		if matchesModuleName(importPath, spModName) {
			return isValidPackageDir(spModRoot)
		}
		if _, ok := hasModulePrefix(importPath, spModName); ok {
			relPath, _ := hasModulePrefix(importPath, spModName)
			return isValidPackageDir(filepath.Join(spModRoot, relPath))
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

// findGalaModRoot searches for gala.mod, preferring the explicit search paths
// over cwd. Returns the directory containing gala.mod, or empty string if not
// found.
//
// When the caller passes search paths (gala_transpile prepends the consumer's
// project directory; gala_bootstrap_transpile passes the staged module root
// for the std files), those paths are the authoritative module roots for this
// build. cwd is whatever the parent shell or build sandbox happens to be —
// for a Bazel genrule it is the *consumer's* execroot, which under
// `local_path_override` may itself contain a `gala.mod` belonging to an
// unrelated module. Letting cwd win there hijacks `moduleRoot` for the
// transpile of std (or any package whose source files live under the search
// path), causing the analyzer to register the same .gala file under two
// different `DefinedIn` strings and trip GALA-E0011 ("type X redefined").
//
// cwd is still consulted as a fallback for callers that pass no search paths
// (legacy CLI invocations from inside a project directory).
func findGalaModRoot(searchPaths []string) string {
	// Prefer explicit search paths — these are authoritative when set.
	for _, sp := range searchPaths {
		absPath, err := filepath.Abs(sp)
		if err != nil {
			continue
		}
		if root := findGalaModFromDir(absPath); root != "" {
			return root
		}
	}

	// Fall back to cwd when no search paths were provided (or none yielded a
	// gala.mod).
	cwd, _ := os.Getwd()
	if root := findGalaModFromDir(cwd); root != "" {
		return root
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

// findModuleRootFromCwdOrPaths searches for go.mod, preferring the explicit
// search paths over cwd. Falls back to cwd only when no search paths were
// provided (or none yielded a go.mod).
//
// Same rationale as findGalaModRoot: when the caller passes search paths,
// those are authoritative for the module being transpiled. cwd may be a
// downstream consumer's Bazel execroot whose go.mod belongs to an unrelated
// module — picking it up there hijacks moduleRoot for transpiles whose
// source files actually live under the search path. This bites the
// gala_bootstrap path in particular: gala_simple ships only go.mod (no
// gala.mod) at its repo root, so the bootstrap genrule for std/*.gala
// reaches this fallback after findGalaModRoot returns empty, and a
// consumer's go.mod staged at execroot/_main/go.mod takes over moduleRoot
// — causing GALA-E0011 "type X redefined" once the std file is registered
// under two different DefinedIn strings.
func findModuleRootFromCwdOrPaths(searchPaths []string) (moduleRoot, moduleName string) {
	// Prefer explicit search paths — these are authoritative when set.
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

	// Fall back to cwd when no search paths yielded a go.mod (legacy CLI
	// invocations from inside a project directory).
	cwd, _ := os.Getwd()
	moduleRoot, moduleName = FindModuleRoot(cwd)
	if moduleRoot != "" {
		return moduleRoot, moduleName
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

// findModuleRootForSearchPath identifies the module root that owns a given
// search path. It prefers a gala.mod found at the search path or any of its
// ancestors (walking up) over a walked-up go.mod, so a GALA dep that lives
// under an unrelated parent directory containing a stray go.mod is not
// misclassified — common pitfalls include the system temp directory sitting
// under someone's GOPATH, and Bazel's execroot junctions exposing unrelated
// workspace module files. Falls back to FindModuleRoot's go.mod walk-up
// when no gala.mod is reachable from sp.
func findModuleRootForSearchPath(sp string) (moduleRoot, moduleName string) {
	if galaModDir := findGalaModFromDir(sp); galaModDir != "" {
		if root, name := findGalaModuleRoot(galaModDir); name != "" {
			return root, name
		}
	}
	return FindModuleRoot(sp)
}

// findGalaModuleRoot looks for gala.mod in the given directory (not walking up)
// and extracts the module name from it. Used as fallback when go.mod is not found
// (pure GALA packages that don't have a go.mod).
func findGalaModuleRoot(startPath string) (moduleRoot, moduleName string) {
	dir := startPath
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	modPath := filepath.Join(dir, "gala.mod")
	content, err := os.ReadFile(modPath)
	if err != nil {
		return "", ""
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			moduleName = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			return dir, moduleName
		}
	}

	return "", ""
}

// isValidPackageDir checks if a directory exists and could contain a package.
func isValidPackageDir(dirPath string) bool {
	info, err := os.Stat(dirPath)
	return err == nil && info.IsDir()
}

// findPackageDirByLongestSuffix walks root searching for a directory whose
// trailing path segments match the longest possible suffix of importPath and
// that contains at least one .gala file. Trying suffixes in decreasing length
// disambiguates two directories that share a base name: an importpath of
// "user/foo/bar" prefers a directory ending in "foo/bar" over a directory
// ending in just "bar" elsewhere in the tree.
//
// When the import path has more than one segment, the search refuses to fall
// back to matching only the final segment. A multi-segment path where every
// multi-segment suffix is missing is almost certainly pointing at an entirely
// different package than any unrelated namesake elsewhere in the tree, and
// returning the namesake silently picks the wrong sources — which surfaces
// downstream as a confusing
// "extractor 'X' must define an Unapply method" error when the consumer tries
// to pattern-match a sealed case that lives in the package they actually
// imported.
func findPackageDirByLongestSuffix(root, importPath string) string {
	parts := splitImportPath(importPath)
	// Stop one short of the single-segment fallback when the import path has
	// more than one segment — see comment above.
	stop := len(parts)
	if len(parts) > 1 {
		stop = len(parts) - 1
	}
	for i := 0; i < stop; i++ {
		suffix := parts[i:]
		if found := findPackageDirByPathSuffix(root, suffix); found != "" {
			return found
		}
	}
	return ""
}

// splitImportPath splits an import path into segments using forward slashes.
func splitImportPath(importPath string) []string {
	if importPath == "" {
		return nil
	}
	return strings.Split(importPath, "/")
}

// findPackageDirByPathSuffix recursively searches under root for a directory
// whose trailing path segments equal `suffix` and that contains at least one
// .gala file. Returns the first match found, or empty string if none found.
func findPackageDirByPathSuffix(root string, suffix []string) string {
	if len(suffix) == 0 {
		return ""
	}
	pkgName := suffix[len(suffix)-1]

	skipDirs := map[string]bool{
		"bazel-out": true, "bazel-bin": true, "bazel-testlogs": true,
		".git": true, ".idea": true, ".ijwb": true, ".bazelbsp": true,
		"node_modules": true, "external": true,
	}

	matchesSuffix := func(path string) bool {
		// Walk up `path`'s components and verify the last len(suffix) segments
		// equal suffix.
		segs := strings.Split(filepath.ToSlash(path), "/")
		if len(segs) < len(suffix) {
			return false
		}
		for i := range suffix {
			if segs[len(segs)-len(suffix)+i] != suffix[i] {
				return false
			}
		}
		return true
	}

	var walk func(dir string) string
	walk = func(dir string) string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return ""
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())

			isDir := e.IsDir()
			if !isDir {
				if info, err := os.Stat(path); err == nil {
					isDir = info.IsDir()
				}
			}
			if !isDir {
				continue
			}
			if skipDirs[e.Name()] {
				continue
			}
			if e.Name() == pkgName && matchesSuffix(path) && hasGalaFiles(path) {
				return path
			}
			if found := walk(path); found != "" {
				return found
			}
		}
		return ""
	}
	return walk(root)
}

// findPackageDirByName recursively searches under root for a directory whose base name
// matches pkgName and that contains at least one .gala file. Returns the first match
// found, or empty string if none found.
func findPackageDirByName(root, pkgName string) string {
	// skipDirs are directories that should not be traversed during the recursive search.
	skipDirs := map[string]bool{
		"bazel-out": true, "bazel-bin": true, "bazel-testlogs": true,
		".git": true, ".idea": true, ".ijwb": true, ".bazelbsp": true,
		"node_modules": true, "external": true,
	}

	// Custom recursive walk that follows symlinks (filepath.Walk/WalkDir do NOT
	// follow symlinks, which breaks in Bazel's execroot where source directories
	// are symlinked to the actual workspace).
	var walk func(dir string) string
	walk = func(dir string) string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return ""
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())

			// Resolve symlinks/junctions to determine if this is a directory.
			// On Windows, os.ReadDir may not detect symlinks or junctions via
			// ModeSymlink, so always fall back to os.Stat when IsDir is false.
			isDir := e.IsDir()
			if !isDir {
				if info, err := os.Stat(path); err == nil {
					isDir = info.IsDir()
				}
			}

			if !isDir {
				continue
			}
			if skipDirs[e.Name()] {
				continue
			}
			if e.Name() == pkgName && hasGalaFiles(path) {
				return path
			}
			// Recurse into subdirectories
			if found := walk(path); found != "" {
				return found
			}
		}
		return ""
	}

	return walk(root)
}

// hasGalaFiles checks if a directory contains at least one .gala file.
func hasGalaFiles(dirPath string) bool {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".gala") {
			return true
		}
	}
	return false
}

// isGalaDir reports whether the given directory contains a gala.mod or
// any .gala source file (signalling that the resolved replacement points
// at a real GALA package, not at a Go-only directory).
func isGalaDir(dirPath string) bool {
	if !isValidPackageDir(dirPath) {
		return false
	}
	if _, err := os.Stat(filepath.Join(dirPath, "gala.mod")); err == nil {
		return true
	}
	return hasGalaFiles(dirPath)
}
