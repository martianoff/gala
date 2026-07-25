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

// buildSubst maps a declaration's type-parameter names to the type arguments of
// a particular instantiation. Returns nil when there is nothing to substitute,
// which substitute() treats as identity.
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

// substitute replaces type-parameter references inside t according to subst,
// walking every composite kind so a field like Array[T] becomes Array[int]. A
// nil/empty subst is identity. Type parameters appear as BasicType (see
// resolveTypeWithParams, which lowers a bare type-param name to BasicType).
func substitute(t transpiler.Type, subst map[string]transpiler.Type) transpiler.Type {
	if len(subst) == 0 || transpiler.IsUnusable(t) {
		return t
	}
	switch v := t.(type) {
	case transpiler.BasicType:
		if repl, ok := subst[v.Name]; ok {
			return repl
		}
		return v
	case transpiler.NamedType:
		// A NamedType with a package is never a type parameter; but an
		// unqualified one might shadow a param name in odd cases.
		if v.Package == "" {
			if repl, ok := subst[v.Name]; ok {
				return repl
			}
		}
		return v
	case transpiler.GenericType:
		params := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = substitute(p, subst)
		}
		return transpiler.GenericType{Base: substitute(v.Base, subst), Params: params}
	case transpiler.ArrayType:
		return transpiler.ArrayType{Elem: substitute(v.Elem, subst)}
	case transpiler.MapType:
		return transpiler.MapType{Key: substitute(v.Key, subst), Elem: substitute(v.Elem, subst)}
	case transpiler.PointerType:
		return transpiler.PointerType{Elem: substitute(v.Elem, subst)}
	case transpiler.FuncType:
		params := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = substitute(p, subst)
		}
		results := make([]transpiler.Type, len(v.Results))
		for i, r := range v.Results {
			results[i] = substitute(r, subst)
		}
		return transpiler.FuncType{Params: params, Results: results}
	}
	return t
}
