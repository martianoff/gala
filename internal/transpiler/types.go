package transpiler

import (
	"strings"
)

// Type represents a structured type in GALA/Go.
//
// Predicates (IsNil, IsAny, IsVoid) are exposed as interface methods
// rather than ad-hoc type assertions so callers can check kind without
// importing the concrete type definitions and without hard-coded
// string comparisons. Each implementation returns the obvious answer
// for its kind; only the matching variant returns true.
type Type interface {
	String() string
	IsNil() bool
	IsAny() bool
	IsVoid() bool
	BaseName() string
	GetPackage() string // Returns the package of the type, or "" if none
}

// IsUnusable reports whether t cannot drive type-directed code
// generation: either the value is nil (no type info at all) or it
// resolved to NilType (the analyzer's "I gave up" placeholder).
//
// Centralizes the `t == nil || t.IsNil()` pattern that appeared in
// 25+ call sites across the transformer. Inline copies were always
// vulnerable to forgetting the nil check before dereferencing into
// IsNil(), which would panic on a nil interface — the helper makes
// that impossible.
func IsUnusable(t Type) bool {
	return t == nil || t.IsNil()
}

// IsUnusableOrAny extends IsUnusable to also reject the unparametrized
// `any` placeholder. Use this in type-inference contexts where falling
// back to `any` would defeat the inference (e.g. resolving a generic
// param from a call-site arg type — `any` carries no information).
func IsUnusableOrAny(t Type) bool {
	return t == nil || t.IsNil() || t.IsAny()
}

// BasicType represents a basic type like int, string, bool.
type BasicType struct {
	Name string
}

func (t BasicType) String() string     { return t.Name }
func (t BasicType) IsNil() bool        { return false }
func (t BasicType) IsAny() bool        { return t.Name == "any" }
func (t BasicType) IsVoid() bool       { return false }
func (t BasicType) BaseName() string   { return t.Name }
func (t BasicType) GetPackage() string { return "" }

// NamedType represents a named type, potentially package-qualified.
type NamedType struct {
	Package    string
	Name       string
	ImportPath string // Full Go import path (e.g., "io/fs"), used for transitive imports from Go type analysis
}

func (t NamedType) String() string {
	if t.Package != "" {
		return t.Package + "." + t.Name
	}
	return t.Name
}
func (t NamedType) IsNil() bool        { return false }
func (t NamedType) IsAny() bool        { return false }
func (t NamedType) IsVoid() bool       { return false }
func (t NamedType) BaseName() string   { return t.String() }
func (t NamedType) GetPackage() string { return t.Package }

// GenericType represents a generic type like Immutable[int] or Tuple[int, string].
type GenericType struct {
	Base   Type
	Params []Type
}

func (t GenericType) String() string {
	var sb strings.Builder
	sb.WriteString(t.Base.String())
	sb.WriteByte('[')
	for i, p := range t.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		if p != nil {
			sb.WriteString(p.String())
		}
	}
	sb.WriteByte(']')
	return sb.String()
}
func (t GenericType) IsNil() bool        { return false }
func (t GenericType) IsAny() bool        { return false }
func (t GenericType) IsVoid() bool       { return false }
func (t GenericType) BaseName() string   { return t.Base.BaseName() }
func (t GenericType) GetPackage() string { return t.Base.GetPackage() }

// ArrayType represents a slice or array type.
type ArrayType struct {
	Elem Type
}

func (t ArrayType) String() string {
	if t.Elem == nil {
		return "[]"
	}
	return "[]" + t.Elem.String()
}
func (t ArrayType) IsNil() bool        { return false }
func (t ArrayType) IsAny() bool        { return false }
func (t ArrayType) IsVoid() bool       { return false }
func (t ArrayType) BaseName() string   { return "[]" + t.Elem.BaseName() }
func (t ArrayType) GetPackage() string { return "" }

// MapType represents a map type.
type MapType struct {
	Key  Type
	Elem Type
}

func (t MapType) String() string {
	return "map[" + t.Key.String() + "]" + t.Elem.String()
}
func (t MapType) IsNil() bool        { return false }
func (t MapType) IsAny() bool        { return false }
func (t MapType) IsVoid() bool       { return false }
func (t MapType) BaseName() string   { return "map" }
func (t MapType) GetPackage() string { return "" }

// PointerType represents a pointer type.
type PointerType struct {
	Elem Type
}

func (t PointerType) String() string {
	return "*" + t.Elem.String()
}
func (t PointerType) IsNil() bool        { return false }
func (t PointerType) IsAny() bool        { return false }
func (t PointerType) IsVoid() bool       { return false }
func (t PointerType) BaseName() string   { return "*" + t.Elem.BaseName() }
func (t PointerType) GetPackage() string { return "" }

// FuncType represents a function type.
type FuncType struct {
	Params  []Type
	Results []Type
}

func (t FuncType) String() string {
	var b strings.Builder
	b.WriteString("func(")
	for i, p := range t.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.String())
	}
	b.WriteString(")")
	if len(t.Results) == 1 {
		b.WriteString(" ")
		b.WriteString(t.Results[0].String())
	} else if len(t.Results) > 1 {
		b.WriteString(" (")
		for i, r := range t.Results {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(r.String())
		}
		b.WriteString(")")
	}
	return b.String()
}
func (t FuncType) IsNil() bool        { return false }
func (t FuncType) IsAny() bool        { return false }
func (t FuncType) IsVoid() bool       { return false }
func (t FuncType) BaseName() string   { return "func" }
func (t FuncType) GetPackage() string { return "" }

// NilType represents an unknown or nil type.
type NilType struct{}

func (t NilType) String() string     { return "" }
func (t NilType) IsNil() bool        { return true }
func (t NilType) IsAny() bool        { return false }
func (t NilType) IsVoid() bool       { return false }
func (t NilType) BaseName() string   { return "" }
func (t NilType) GetPackage() string { return "" }

// VoidType represents expressions used purely for side effects (no return value).
// Used for match branches that call functions like fmt.Printf that return multiple values
// or when the return value is not meaningful.
type VoidType struct{}

func (t VoidType) String() string     { return "void" }
func (t VoidType) IsNil() bool        { return false }
func (t VoidType) IsAny() bool        { return false }
func (t VoidType) IsVoid() bool       { return true }
func (t VoidType) BaseName() string   { return "void" }
func (t VoidType) GetPackage() string { return "" }

// IsPrimitiveType checks if a type name is a Go primitive/builtin type.
// Primitive types should never be package-qualified.
func IsPrimitiveType(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64",
		"complex64", "complex128",
		"bool", "byte", "rune", "string",
		"any", "error":
		return true
	}
	return false
}

// ParseType is a helper to transition from string-based types to structured types.
// It should be used sparingly as we want the analyzer to produce structured types directly.
func ParseType(s string) Type {
	s = strings.TrimSpace(s)
	if s == "" {
		return NilType{}
	}
	if strings.HasPrefix(s, "[]") {
		return ArrayType{Elem: ParseType(s[2:])}
	}
	if strings.HasPrefix(s, "*") {
		return PointerType{Elem: ParseType(s[1:])}
	}
	// Handle package-prefixed pointer types like "pkg.*Type" or "pkg.*Type[T]"
	if idx := strings.Index(s, ".*"); idx != -1 {
		pkg := s[:idx]
		rest := s[idx+2:] // Skip ".*"
		innerType := ParseType(pkg + "." + rest)
		return PointerType{Elem: innerType}
	}
	if strings.HasPrefix(s, "map[") {
		// Very simple map parsing, doesn't handle nested maps well
		closingBracket := strings.Index(s, "]")
		if closingBracket != -1 {
			key := ParseType(s[4:closingBracket])
			elem := ParseType(s[closingBracket+1:])
			return MapType{Key: key, Elem: elem}
		}
	}
	if strings.Contains(s, "[") && strings.HasSuffix(s, "]") {
		idx := strings.Index(s, "[")
		base := ParseType(s[:idx])
		paramsStr := s[idx+1 : len(s)-1]

		// Split by comma, respecting nested brackets
		var params []Type
		bracketCount := 0
		start := 0
		for i := 0; i < len(paramsStr); i++ {
			switch paramsStr[i] {
			case '[':
				bracketCount++
			case ']':
				bracketCount--
			case ',':
				if bracketCount == 0 {
					params = append(params, ParseType(paramsStr[start:i]))
					start = i + 1
				}
			}
		}
		params = append(params, ParseType(paramsStr[start:]))
		return GenericType{Base: base, Params: params}
	}
	if idx := strings.LastIndex(s, "."); idx != -1 {
		// Check if it's not a float literal (though ParseType shouldn't be called on literals)
		pkg := s[:idx]
		name := s[idx+1:]
		return NamedType{Package: pkg, Name: name}
	}
	return BasicType{Name: s}
}
