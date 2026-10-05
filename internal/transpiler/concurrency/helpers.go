package concurrency

import (
	"strings"

	"martianoff/gala/internal/transpiler"
)

// Package names of the two collection libraries. Their types share names
// (Array, List, HashMap, HashSet, TreeMap, TreeSet), so only the package
// qualifier tells immutable from mutable.
const (
	pkgCollectionImmutable = "collection_immutable"
	pkgCollectionMutable   = "collection_mutable"
)

// isShareablePrimitive reports whether name is a value-kinded primitive that is
// trivially shareable (deeply immutable, no shared backing store).
//
// Deliberately EXCLUDES:
//   - "any" — an interface that can hold any (possibly mutable) dynamic value.
//   - "error" — also an interface; a concrete error may close over mutable state.
//   - "uintptr" — a raw address escape hatch, not a value we reason about.
func isShareablePrimitive(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64",
		"complex64", "complex128",
		"bool", "string", "byte", "rune",
		// Unit sometimes surfaces as a bare name rather than VoidType.
		"Unit", "unit", "void":
		return true
	}
	return false
}

// stripPkg drops a leading package qualifier from a type name, e.g.
// "std.Option" -> "Option". Names without a dot are returned unchanged.
func stripPkg(name string) string {
	if idx := strings.LastIndex(name, "."); idx != -1 {
		return name[idx+1:]
	}
	return name
}

// metaKey builds a stable identity key for a resolved type's metadata, used as
// the visited-set entry that breaks recursion on self-referential types.
func metaKey(meta *transpiler.TypeMetadata) string {
	if meta.Package != "" {
		return meta.Package + "." + meta.Name
	}
	return meta.Name
}

// instantiationKey renders type arguments into a stable suffix for the
// visited-set key, so two instantiations of the same generic type (Node[int]
// vs Node[MutableArray[int]]) get DISTINCT keys and the recursion only
// terminates on a genuine same-argument self-reference. Empty args yield "",
// so non-generic types keep their bare metaKey.
func instantiationKey(args []transpiler.Type) string {
	if len(args) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, a := range args {
		if i > 0 {
			b.WriteByte(',')
		}
		if !transpiler.IsUnusable(a) {
			b.WriteString(a.String())
		}
	}
	b.WriteByte(']')
	return b.String()
}

// buildSubst maps a declaration's type-parameter names to the type arguments of
// a particular instantiation. Returns nil when there is nothing to substitute,
// which transpiler.SubstituteTypeParams treats as identity.
func buildSubst(typeParams []string, typeArgs []transpiler.Type) map[string]transpiler.Type {
	if len(typeParams) == 0 || len(typeArgs) == 0 {
		return nil
	}
	m := make(map[string]transpiler.Type, len(typeParams))
	for i, tp := range typeParams {
		if i < len(typeArgs) {
			m[tp] = typeArgs[i]
		}
	}
	return m
}
