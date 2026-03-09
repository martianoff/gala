// Package resolver provides shared type name resolution logic used by both
// the analyzer and transformer. This ensures consistent precedence rules
// when resolving unqualified type names to their fully-qualified forms.
package resolver

import (
	"martianoff/gala/internal/transpiler/registry"
)

// PackageInfo describes an imported package for type resolution purposes.
type PackageInfo struct {
	PkgName string // Actual package name (e.g., "collection_immutable")
	IsDot   bool   // Whether this is a dot import
}

// TypeResolver resolves unqualified type names using a consistent precedence order.
// The resolution order is:
//  1. Exact match (bare name)
//  2. std package prefix (for standard library types like Option, Tuple, etc.)
//  3. Current package prefix
//  4. Dot-imported packages (they bring names into scope)
//  5. Named (non-dot) imported packages
type TypeResolver struct {
	// PackageName is the current package being compiled.
	PackageName string

	// Imports lists all imported packages, in declaration order.
	// Dot imports are tried before named imports per the precedence rules.
	Imports []PackageInfo
}

// Resolve attempts to resolve an unqualified type name by trying various
// package prefixes in precedence order. The exists function is called with
// each candidate fully-qualified name to check if it exists in the target
// data structure (e.g., typeMetas map, richAST.Types map).
//
// Returns the resolved name and true if found, or ("", false) if not.
func (r *TypeResolver) Resolve(name string, exists func(string) bool) (string, bool) {
	// 1. Try bare name without any package prefix
	if exists(name) {
		return name, true
	}

	// 2. Try std package prefix for standard library types
	if stdName := registry.StdPackageName + "." + name; exists(stdName) {
		return stdName, true
	}

	// 3. Try current package prefix
	if r.PackageName != "" {
		if fullName := r.PackageName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	// 4. Try dot-imported packages (they bring names into the current scope,
	// so they take precedence over non-dot imports)
	for _, imp := range r.Imports {
		if !imp.IsDot {
			continue
		}
		if fullName := imp.PkgName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	// 5. Try named (non-dot) imported packages
	for _, imp := range r.Imports {
		if imp.IsDot {
			continue
		}
		if fullName := imp.PkgName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	return "", false
}
