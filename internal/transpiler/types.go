package transpiler

import (
	"go/token"
	"strconv"
	"strings"

	"martianoff/gala/internal/parser"
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

// HasResult reports whether t, a declared result type, is a value: known and
// not void. A declaration with no result records VoidType.
func HasResult(t Type) bool {
	return !IsUnusable(t) && !t.IsVoid()
}

// ContainsUnusable reports whether t is unusable or holds an unusable type
// anywhere inside it: go/types resolves `[]box.Box` whose package failed to
// load to a slice whose ELEMENT is unresolved, so a top-level check alone
// would let `[]<nothing>` through.
func ContainsUnusable(t Type) bool { return containsType(t, IsUnusable) }

// ContainsStruct reports whether t is, or holds anywhere inside it, an
// anonymous Go struct type (StructType), which GALA cannot write.
func ContainsStruct(t Type) bool { return containsType(t, isStructType) }

func isStructType(t Type) bool {
	_, ok := t.(StructType)
	return ok
}

// containsType reports whether match holds for t or for any type inside it.
func containsType(t Type, match func(Type) bool) bool {
	if match(t) {
		return true
	}
	switch v := t.(type) {
	case ArrayType:
		return containsType(v.Elem, match)
	case ChanType:
		return containsType(v.Elem, match)
	case StructType, InterfaceType:
		return anyContainsType(MemberTypes(v), match)
	case PointerType:
		return containsType(v.Elem, match)
	case MapType:
		return containsType(v.Key, match) || containsType(v.Elem, match)
	case GenericType:
		return containsType(v.Base, match) || anyContainsType(v.Params, match)
	case FuncType:
		return anyContainsType(v.Params, match) || anyContainsType(v.Results, match)
	}
	return false
}

// anyContainsType reports whether containsType(t, match) holds for any t in ts.
func anyContainsType(ts []Type, match func(Type) bool) bool {
	for _, t := range ts {
		if containsType(t, match) {
			return true
		}
	}
	return false
}

// IsUnusableOrAny extends IsUnusable to also reject the unparametrized
// `any` placeholder. Use this in type-inference contexts where falling
// back to `any` would defeat the inference (e.g. resolving a generic
// param from a call-site arg type — `any` carries no information).
func IsUnusableOrAny(t Type) bool {
	return t == nil || t.IsNil() || t.IsAny()
}

// UnwrapSendable reports whether t is the transparent boundary marker
// `Sendable[F]` and, if so, returns its single inner type F. For any other
// type it returns (t, false). The base-name comparison is package-agnostic so
// `std.Sendable[F]`, a dot-imported `Sendable[F]`, and a library's own
// re-export all unwrap identically. This is the single point that gives the
// marker its "behaves exactly like F" transparency: every typing and codegen
// site that might see a Sendable type first unwraps it here.
func UnwrapSendable(t Type) (Type, bool) {
	g, ok := t.(GenericType)
	if !ok || len(g.Params) != 1 {
		return t, false
	}
	name := g.Base.BaseName()
	if idx := strings.LastIndex(name, "."); idx != -1 {
		name = name[idx+1:]
	}
	if name != TypeSendable {
		return t, false
	}
	return g.Params[0], true
}

// IsPtr reports whether t is GALA's non-null pointer `Ptr[T]`.
func IsPtr(t Type) bool {
	p, ok := t.(PointerType)
	return ok && p.NonNull
}

// IsNullablePtr reports whether t is a plain, nullable `*T`.
func IsNullablePtr(t Type) bool {
	p, ok := t.(PointerType)
	return ok && !p.NonNull
}

// DisplayType is t as GALA source spells it: a non-null pointer reads
// `Ptr[T]` where String reads `*T`. Every other kind reads as String does.
func DisplayType(t Type) string {
	return DisplayTypeNamed(t, func(leaf Type) string { return leaf.String() })
}

// DisplayTypeNamed is DisplayType with each named or basic type, the leaves
// of t, spelled by name: a diagnostic qualifies them as its file would.
func DisplayTypeNamed(t Type, name func(leaf Type) string) string {
	switch v := t.(type) {
	case nil:
		return ""
	case PointerType:
		if v.NonNull {
			return parser.PtrTypeName + "[" + DisplayTypeNamed(v.Elem, name) + "]"
		}
		return "*" + DisplayTypeNamed(v.Elem, name)
	case ArrayType:
		return v.Prefix() + DisplayTypeNamed(v.Elem, name)
	case ChanType:
		return spellChan(v.Dir, v.Elem, DisplayTypeNamed(v.Elem, name))
	case StructType:
		return spellStruct(v, func(f Type) string { return DisplayTypeNamed(f, name) })
	case InterfaceType:
		return spellInterface(v, func(m Type) string { return DisplayTypeNamed(m, name) })
	case MapType:
		return "map[" + DisplayTypeNamed(v.Key, name) + "]" + DisplayTypeNamed(v.Elem, name)
	case GenericType:
		return DisplayTypeNamed(v.Base, name) + "[" + displayTypes(v.Params, name) + "]"
	case FuncType:
		s := "func(" + displayTypes(v.Params, name) + ")"
		switch len(v.Results) {
		case 0:
			return s
		case 1:
			return s + " " + DisplayTypeNamed(v.Results[0], name)
		}
		return s + " (" + displayTypes(v.Results, name) + ")"
	case NamedType, BasicType:
		return name(t)
	}
	return t.String()
}

// displayTypes is DisplayTypeNamed of each of ts, comma-separated.
func displayTypes(ts []Type, name func(leaf Type) string) string {
	parts := make([]string, len(ts))
	for i, p := range ts {
		parts[i] = DisplayTypeNamed(p, name)
	}
	return strings.Join(parts, ", ")
}

// IsSendable reports whether t is a `Sendable[F]` boundary marker.
func IsSendable(t Type) bool {
	_, ok := UnwrapSendable(t)
	return ok
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

// ArrayType represents a slice type, or, when Fixed, a Go array type of
// length Len, `[Len]Elem`. A fixed-length array reaches GALA as the type of
// an anonymous Go struct's field, which must be spelled exactly.
type ArrayType struct {
	Elem  Type
	Len   int64
	Fixed bool
}

// Prefix is the spelling ahead of t's element type: `[]` or `[Len]`.
func (t ArrayType) Prefix() string {
	if t.Fixed {
		return "[" + strconv.FormatInt(t.Len, 10) + "]"
	}
	return "[]"
}

func (t ArrayType) String() string {
	if t.Elem == nil {
		return t.Prefix()
	}
	return t.Prefix() + t.Elem.String()
}
func (t ArrayType) IsNil() bool        { return false }
func (t ArrayType) IsAny() bool        { return false }
func (t ArrayType) IsVoid() bool       { return false }
func (t ArrayType) BaseName() string   { return t.Prefix() + t.Elem.BaseName() }
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

// ChanDir is the direction of a channel type.
type ChanDir int

const (
	ChanBoth ChanDir = iota // chan T
	ChanRecv                // <-chan T
	ChanSend                // chan<- T
)

// chanPrefixes is the spelling of each direction ahead of the element type.
var chanPrefixes = [...]string{ChanBoth: "chan ", ChanRecv: "<-chan ", ChanSend: "chan<- "}

// spellChan is a channel of dir whose element elem is spelled elemText. Go
// reads `chan <-chan T` as `chan<- (chan T)`, so a receive-only element of a
// bidirectional channel is parenthesized, as go/types prints it.
func spellChan(dir ChanDir, elem Type, elemText string) string {
	if e, ok := elem.(ChanType); ok && dir == ChanBoth && e.Dir == ChanRecv {
		elemText = "(" + elemText + ")"
	}
	return chanPrefixes[dir] + elemText
}

// ChanType represents a Go channel type. GALA code cannot spell one; it gets
// channels from Go functions and receives from them with `<-ch`, which yields
// Elem.
type ChanType struct {
	Elem Type
	Dir  ChanDir
}

func (t ChanType) String() string {
	return spellChan(t.Dir, t.Elem, t.Elem.String())
}
func (t ChanType) IsNil() bool        { return false }
func (t ChanType) IsAny() bool        { return false }
func (t ChanType) IsVoid() bool       { return false }
func (t ChanType) BaseName() string   { return "chan" }
func (t ChanType) GetPackage() string { return "" }

// StructField is a field of a StructType. An embedded field is named after
// its type, as go/types names it.
type StructField struct {
	Name     string
	Type     Type
	Embedded bool
	Tag      string
}

// StructType represents an anonymous Go struct type, `struct{}` or
// `struct{X int}`. GALA code cannot spell one; it reaches GALA from Go APIs,
// most often as the element of a signal channel (`chan struct{}`).
type StructType struct {
	Fields []StructField
}

func (t StructType) String() string { return spellStruct(t, Type.String) }
func (t StructType) IsNil() bool    { return false }
func (t StructType) IsAny() bool    { return false }
func (t StructType) IsVoid() bool   { return false }

// BaseName is "struct", as a channel's is "chan": an anonymous struct names
// no declaration, and its spelling holds dots a package split would misread.
func (t StructType) BaseName() string   { return "struct" }
func (t StructType) GetPackage() string { return "" }

// FieldTypes is the type of each of t's fields, in order.
func (t StructType) FieldTypes() []Type {
	types := make([]Type, len(t.Fields))
	for i, f := range t.Fields {
		types[i] = f.Type
	}
	return types
}

// MapFields is t with each field's type replaced by f of it.
func (t StructType) MapFields(f func(Type) Type) StructType {
	if len(t.Fields) == 0 {
		return t
	}
	fields := make([]StructField, len(t.Fields))
	for i, fd := range t.Fields {
		fd.Type = f(fd.Type)
		fields[i] = fd
	}
	return StructType{Fields: fields}
}

// spellStruct is t as go/types prints it, `struct{X int; Y string "tag"}`,
// with each field's type spelled by name.
func spellStruct(t StructType, name func(Type) string) string {
	parts := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		s := name(f.Type)
		if !f.Embedded {
			s = f.Name + " " + s
		}
		if f.Tag != "" {
			s += " " + strconv.Quote(f.Tag)
		}
		parts[i] = s
	}
	return "struct{" + strings.Join(parts, "; ") + "}"
}

// parseStruct parses the body of `struct{...}` in the form spellStruct
// prints. A field's tag is its quote outside any brackets: a quote inside
// them belongs to the tag of a nested struct's field.
func parseStruct(body string) StructType {
	var st StructType
	for _, part := range splitTopLevelOn(body, ';') {
		var f StructField
		if q := topLevelQuote(part); q != -1 {
			f.Tag, _ = strconv.Unquote(strings.TrimSpace(part[q:]))
			part = strings.TrimSpace(part[:q])
		}
		name, typ, named := strings.Cut(part, " ")
		if named && token.IsIdentifier(name) {
			f.Name, f.Type = name, ParseType(typ)
		} else {
			f.Type, f.Embedded = ParseType(part), true
			f.Name = EmbeddedFieldName(f.Type)
		}
		st.Fields = append(st.Fields, f)
	}
	return st
}

// topLevelQuote is the index of s's first quote outside brackets, or -1.
func topLevelQuote(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			if depth == 0 {
				return i
			}
			i = closingQuote(s, i)
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
	}
	return -1
}

// EmbeddedFieldName is the name Go gives a field embedding typ: its type
// name, without a pointer, package or type arguments.
func EmbeddedFieldName(typ Type) string {
	switch v := typ.(type) {
	case PointerType:
		return EmbeddedFieldName(v.Elem)
	case GenericType:
		return EmbeddedFieldName(v.Base)
	case NamedType:
		return v.Name
	}
	return typ.String()
}

// InterfaceMethod is a method of an InterfaceType.
type InterfaceMethod struct {
	Name string
	Type FuncType
}

// InterfaceType represents an inline, non-empty Go interface type,
// `interface{Area() int}`, by its method set: an embedded interface's
// methods are listed as its own, which is the same type. GALA code cannot
// spell one; it reaches GALA as the type of an anonymous Go struct's field.
// The empty interface is BasicType `any`.
type InterfaceType struct {
	Methods []InterfaceMethod
}

func (t InterfaceType) String() string { return spellInterface(t, Type.String) }
func (t InterfaceType) IsNil() bool    { return false }
func (t InterfaceType) IsAny() bool    { return false }
func (t InterfaceType) IsVoid() bool   { return false }

// BaseName is "interface", as an anonymous struct's is "struct".
func (t InterfaceType) BaseName() string   { return "interface" }
func (t InterfaceType) GetPackage() string { return "" }

// MethodTypes is the signature of each of t's methods, in order.
func (t InterfaceType) MethodTypes() []Type {
	types := make([]Type, len(t.Methods))
	for i, m := range t.Methods {
		types[i] = m.Type
	}
	return types
}

// Method is t's method named name.
func (t InterfaceType) Method(name string) (FuncType, bool) {
	for _, m := range t.Methods {
		if m.Name == name {
			return m.Type, true
		}
	}
	return FuncType{}, false
}

// MapMethods is t with each method's signature replaced by f of it. A
// replacement that is not a signature leaves the method as it was.
func (t InterfaceType) MapMethods(f func(Type) Type) InterfaceType {
	if len(t.Methods) == 0 {
		return t
	}
	methods := make([]InterfaceMethod, len(t.Methods))
	for i, m := range t.Methods {
		if ft, ok := f(m.Type).(FuncType); ok {
			m.Type = ft
		}
		methods[i] = m
	}
	return InterfaceType{Methods: methods}
}

// MemberTypes is the type of each field of a StructType, or the signature of
// each method of an InterfaceType, in order: what a walk over a type visits
// inside either. nil for any other type.
func MemberTypes(t Type) []Type {
	switch v := t.(type) {
	case StructType:
		return v.FieldTypes()
	case InterfaceType:
		return v.MethodTypes()
	}
	return nil
}

// MapMembers is t, a StructType or an InterfaceType, with each member's type
// (see MemberTypes) replaced by f of it. Any other t is returned as it is.
func MapMembers(t Type, f func(Type) Type) Type {
	switch v := t.(type) {
	case StructType:
		return v.MapFields(f)
	case InterfaceType:
		return v.MapMethods(f)
	}
	return t
}

// spellInterface is t as go/types prints it, `interface{M(int) string; N()}`,
// with each method's signature spelled by sig less its `func` keyword.
func spellInterface(t InterfaceType, sig func(Type) string) string {
	parts := make([]string, len(t.Methods))
	for i, m := range t.Methods {
		parts[i] = m.Name + strings.TrimPrefix(sig(m.Type), "func")
	}
	return "interface{" + strings.Join(parts, "; ") + "}"
}

// parseInterface parses the body of `interface{...}` in the form
// spellInterface prints.
func parseInterface(body string) (InterfaceType, bool) {
	var it InterfaceType
	for _, part := range splitTopLevelOn(body, ';') {
		open := strings.IndexByte(part, '(')
		if open <= 0 || !token.IsIdentifier(part[:open]) {
			return InterfaceType{}, false
		}
		ft, ok := parseFuncType("func" + part[open:])
		if !ok {
			return InterfaceType{}, false
		}
		it.Methods = append(it.Methods, InterfaceMethod{Name: part[:open], Type: ft})
	}
	return it, len(it.Methods) > 0
}

// PointerType represents a pointer type.
//
// NonNull marks GALA's non-null pointer `Ptr[T]`: a compile-time marker that
// erases to `*T` in generated Go. String and BaseName, which name lookups
// read, are the same for both; DisplayType spells it `Ptr[T]`, and ParseType
// reads that spelling back. Only the checks that keep a nil out of a `Ptr`
// read the marker (see IsPtr).
type PointerType struct {
	Elem    Type
	NonNull bool
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

// parseFuncType parses `func(P1, P2) R` and `func(P1) (R1, R2)`.
func parseFuncType(s string) (FuncType, bool) {
	closeParen := matchingClose(s, len("func"))
	if closeParen == -1 {
		return FuncType{}, false
	}
	var ft FuncType
	for _, p := range splitTopLevel(s[len("func(") : closeParen]) {
		ft.Params = append(ft.Params, ParseType(p))
	}
	rest := strings.TrimSpace(s[closeParen+1:])
	switch {
	case rest == "":
	case strings.HasPrefix(rest, "(") && matchingClose(rest, 0) == len(rest)-1:
		for _, r := range splitTopLevel(rest[1 : len(rest)-1]) {
			ft.Results = append(ft.Results, ParseType(r))
		}
	default:
		ft.Results = []Type{ParseType(rest)}
	}
	return ft, true
}

// matchingClose returns the index of the bracket closing the one at s[open]
// ('(' or '['), counting every kind of nesting, or -1 when it is unbalanced.
func matchingClose(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			i = closingQuote(s, i)
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// closingQuote is the index of the quote closing the quoted string, a struct
// field's tag, that opens at s[open]: brackets and separators inside it are
// text. len(s) when it is not closed.
func closingQuote(s string, open int) int {
	for i := open + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return len(s)
}

// splitTopLevel splits a comma-separated type list at the commas that are not
// nested inside brackets or parentheses, trimming each part and dropping an
// empty list's single empty part.
func splitTopLevel(s string) []string { return splitTopLevelOn(s, ',') }

// splitTopLevelOn is splitTopLevel at sep: `;` separates a struct's fields.
func splitTopLevelOn(s string, sep byte) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			i = closingQuote(s, i)
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(s[start:]))
}

// ParseType is a helper to transition from string-based types to structured types.
// It should be used sparingly as we want the analyzer to produce structured types directly.
func ParseType(s string) Type {
	s = strings.TrimSpace(s)
	if s == "" {
		return NilType{}
	}
	// An anonymous struct, in the form StructType.String() prints.
	if body, ok := strings.CutPrefix(s, "struct{"); ok && matchingClose(s, len("struct")) == len(s)-1 {
		return parseStruct(body[:len(body)-1])
	}
	// An inline interface, in the form InterfaceType.String() prints.
	if body, ok := strings.CutPrefix(s, "interface{"); ok && matchingClose(s, len("interface")) == len(s)-1 {
		if it, ok := parseInterface(body[:len(body)-1]); ok {
			return it
		}
	}
	// A function type, in the form FuncType.String() prints. Without this a
	// function-typed type argument that round-trips through its string form
	// (`func(int) int`) came back as an opaque name, so calling a value of that
	// type inferred nothing.
	if strings.HasPrefix(s, "func(") {
		if ft, ok := parseFuncType(s); ok {
			return ft
		}
	}
	if strings.HasPrefix(s, "[]") {
		return ArrayType{Elem: ParseType(s[2:])}
	}
	// A fixed-length array, `[3]int`.
	if rest, ok := strings.CutPrefix(s, "["); ok {
		if lenText, elem, ok := strings.Cut(rest, "]"); ok {
			if n, err := strconv.ParseInt(lenText, 10, 64); err == nil && n >= 0 {
				return ArrayType{Elem: ParseType(elem), Len: n, Fixed: true}
			}
		}
	}
	for dir, prefix := range chanPrefixes {
		if elem, ok := strings.CutPrefix(s, prefix); ok {
			if strings.HasPrefix(elem, "(") && strings.HasSuffix(elem, ")") {
				elem = elem[1 : len(elem)-1] // chan (<-chan T), see spellChan
			}
			return ChanType{Elem: ParseType(elem), Dir: ChanDir(dir)}
		}
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
		if closingBracket := matchingClose(s, 3); closingBracket != -1 {
			key := ParseType(s[4:closingBracket])
			elem := ParseType(s[closingBracket+1:])
			return MapType{Key: key, Elem: elem}
		}
	}
	if strings.Contains(s, "[") && strings.HasSuffix(s, "]") {
		idx := strings.Index(s, "[")
		parts := splitTopLevel(s[idx+1 : len(s)-1])
		if parser.IsPtrTypeName(s[:idx]) && len(parts) == 1 {
			return PointerType{Elem: ParseType(parts[0]), NonNull: true}
		}
		base := ParseType(s[:idx])
		if len(parts) == 0 {
			parts = []string{""}
		}
		params := make([]Type, len(parts))
		for i, p := range parts {
			params[i] = ParseType(p)
		}
		return StdTupleOfArity(GenericType{Base: base, Params: params})
	}
	if idx := strings.LastIndex(s, "."); idx != -1 {
		// Check if it's not a float literal (though ParseType shouldn't be called on literals)
		pkg := s[:idx]
		name := s[idx+1:]
		return NamedType{Package: pkg, Name: name}
	}
	return BasicType{Name: s}
}

// SubstituteTypeParams replaces type-parameter references inside t according to subst,
// walking every composite kind so a field like Array[T] becomes Array[int]. A
// nil/empty subst is identity. Type parameters appear as BasicType (see
// resolveTypeWithParams, which lowers a bare type-param name to BasicType).
func SubstituteTypeParams(t Type, subst map[string]Type) Type {
	if len(subst) == 0 || IsUnusable(t) {
		return t
	}
	switch v := t.(type) {
	case BasicType:
		if repl, ok := subst[v.Name]; ok {
			return repl
		}
		return v
	case NamedType:
		// A NamedType with a package is never a type parameter; but an
		// unqualified one might shadow a param name in odd cases.
		if v.Package == "" {
			if repl, ok := subst[v.Name]; ok {
				return repl
			}
		}
		return v
	case GenericType:
		params := make([]Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = SubstituteTypeParams(p, subst)
		}
		return GenericType{Base: SubstituteTypeParams(v.Base, subst), Params: params}
	case ArrayType:
		v.Elem = SubstituteTypeParams(v.Elem, subst)
		return v
	case ChanType:
		return ChanType{Elem: SubstituteTypeParams(v.Elem, subst), Dir: v.Dir}
	case StructType, InterfaceType:
		return MapMembers(v, func(f Type) Type { return SubstituteTypeParams(f, subst) })
	case MapType:
		return MapType{Key: SubstituteTypeParams(v.Key, subst), Elem: SubstituteTypeParams(v.Elem, subst)}
	case PointerType:
		return PointerType{Elem: SubstituteTypeParams(v.Elem, subst), NonNull: v.NonNull}
	case FuncType:
		params := make([]Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = SubstituteTypeParams(p, subst)
		}
		results := make([]Type, len(v.Results))
		for i, r := range v.Results {
			results[i] = SubstituteTypeParams(r, subst)
		}
		return FuncType{Params: params, Results: results}
	}
	return t
}
