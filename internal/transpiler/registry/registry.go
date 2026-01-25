// Package registry provides a unified system for managing prelude packages
// (auto-imported packages like std) and their exports.
//
// This replaces hardcoded std library handling with a generic system that
// treats all packages uniformly, following the GALA design principle that
// the standard library should use the same mechanisms as any other library.
package registry

import (
	"fmt"
	"sync"
)

// PackageInfo describes a registered package and its exports.
type PackageInfo struct {
	Name       string   // Short package name: "std"
	ImportPath string   // Full import path: "martianoff/gala/std"
	Types      []string // Exported type names
	Functions  []string // Exported function names
	Companions []string // Companion object names (used for pattern matching)
	IsPrelude  bool     // If true, automatically imported into every file
}

// PackageRegistry manages known packages and provides lookup capabilities.
// It supports "prelude" packages that are automatically available in all files
// without explicit imports.
//
// Thread-safe: all methods can be called concurrently.
type PackageRegistry struct {
	mu sync.RWMutex

	// prelude maps package name to info for auto-imported packages
	prelude map[string]*PackageInfo

	// typeIndex maps type name to owning package for quick lookup
	typeIndex map[string]*PackageInfo

	// funcIndex maps function name to owning package for quick lookup
	funcIndex map[string]*PackageInfo

	// companionIndex maps companion object name to owning package
	companionIndex map[string]*PackageInfo
}

// NewRegistry creates an empty package registry.
// Use RegisterPrelude to add prelude packages.
func NewRegistry() *PackageRegistry {
	return &PackageRegistry{
		prelude:        make(map[string]*PackageInfo),
		typeIndex:      make(map[string]*PackageInfo),
		funcIndex:      make(map[string]*PackageInfo),
		companionIndex: make(map[string]*PackageInfo),
	}
}

// RegisterPrelude adds a package to the prelude (auto-imported packages).
// All types, functions, and companions from this package become available
// without explicit imports.
func (r *PackageRegistry) RegisterPrelude(info PackageInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	info.IsPrelude = true
	infoCopy := info // Store a copy
	r.prelude[info.Name] = &infoCopy

	// Index all exports for quick lookup
	for _, t := range info.Types {
		r.typeIndex[t] = &infoCopy
	}
	for _, f := range info.Functions {
		r.funcIndex[f] = &infoCopy
	}
	for _, c := range info.Companions {
		r.companionIndex[c] = &infoCopy
	}
}

// IsPreludePackage checks if a package name is a prelude package.
func (r *PackageRegistry) IsPreludePackage(pkgName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.prelude[pkgName]
	return ok
}

// IsPreludeType checks if a type name belongs to a prelude package.
// Returns the package info and true if found, nil and false otherwise.
func (r *PackageRegistry) IsPreludeType(typeName string) (*PackageInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.typeIndex[typeName]
	return info, ok
}

// IsPreludeFunction checks if a function name belongs to a prelude package.
// Returns the package info and true if found, nil and false otherwise.
func (r *PackageRegistry) IsPreludeFunction(funcName string) (*PackageInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.funcIndex[funcName]
	return info, ok
}

// IsPreludeCompanion checks if a companion object name belongs to a prelude package.
// Returns the package info and true if found, nil and false otherwise.
func (r *PackageRegistry) IsPreludeCompanion(name string) (*PackageInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.companionIndex[name]
	return info, ok
}

// GetPreludePackage returns the package info for a prelude package by name.
// Returns nil if not found.
func (r *PackageRegistry) GetPreludePackage(pkgName string) *PackageInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.prelude[pkgName]
}

// PreludePackages returns all registered prelude packages.
func (r *PackageRegistry) PreludePackages() []*PackageInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*PackageInfo, 0, len(r.prelude))
	for _, info := range r.prelude {
		result = append(result, info)
	}
	return result
}

// CheckConflict returns an error if the given name conflicts with a prelude export.
// This prevents user code from shadowing prelude types/functions.
//
// Parameters:
//   - name: the type or function name to check
//   - currentPkg: the package defining this name (prelude packages can define their own exports)
func (r *PackageRegistry) CheckConflict(name, currentPkg string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Check if current package is a prelude package (they can define these names)
	if _, isPrelude := r.prelude[currentPkg]; isPrelude {
		return nil
	}

	// Check for type conflict
	if info, ok := r.typeIndex[name]; ok {
		return &ConflictError{
			Name:        name,
			Kind:        "type",
			PackageName: info.Name,
		}
	}

	// Check for function conflict
	if info, ok := r.funcIndex[name]; ok {
		return &ConflictError{
			Name:        name,
			Kind:        "function",
			PackageName: info.Name,
		}
	}

	// Check for companion conflict
	if info, ok := r.companionIndex[name]; ok {
		return &ConflictError{
			Name:        name,
			Kind:        "companion object",
			PackageName: info.Name,
		}
	}

	return nil
}

// ConflictError is returned when a name conflicts with a prelude export.
type ConflictError struct {
	Name        string // The conflicting name
	Kind        string // "type", "function", or "companion object"
	PackageName string // The prelude package that exports this name
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s '%s' conflicts with %s library export; choose a different name",
		e.Kind, e.Name, e.PackageName)
}

// QualifyName returns the fully qualified name for a prelude type/function.
// For example, "Option" -> "std.Option" if std is the owning prelude package.
// Returns the original name if not a prelude export.
func (r *PackageRegistry) QualifyName(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if info, ok := r.typeIndex[name]; ok {
		return info.Name + "." + name
	}
	if info, ok := r.funcIndex[name]; ok {
		return info.Name + "." + name
	}
	if info, ok := r.companionIndex[name]; ok {
		return info.Name + "." + name
	}
	return name
}

// GetImportPath returns the import path for a prelude type/function.
// Returns empty string if not a prelude export.
func (r *PackageRegistry) GetImportPath(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if info, ok := r.typeIndex[name]; ok {
		return info.ImportPath
	}
	if info, ok := r.funcIndex[name]; ok {
		return info.ImportPath
	}
	if info, ok := r.companionIndex[name]; ok {
		return info.ImportPath
	}
	return ""
}
