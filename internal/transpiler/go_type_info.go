package transpiler

import "strings"

// GoTypeInfo holds type information extracted from Go source files and packages.
// It bridges the gap between Go's type system and GALA's transpiler type system,
// enabling type inference for Go function calls, struct field access, and method calls.
type GoTypeInfo struct {
	// Functions maps "pkg.FuncName" -> function signature
	Functions map[string]*GoFuncSignature
	// Types maps "pkg.TypeName" -> type metadata
	Types map[string]*GoTypeData
	// Variables maps "pkg.VarName" -> type
	Variables map[string]Type
	// Constants maps "pkg.ConstName" -> type. An untyped constant is recorded
	// under its default type (`math.MaxInt8` -> int).
	Constants map[string]Type
	// UntypedConstants holds the "pkg.ConstName" keys of constants declared
	// without a type (`const MaxInt8 = 1<<7 - 1`). Like an untyped literal,
	// such a constant takes the type of the slot it is assigned to, so its
	// Constants entry is only the fallback default.
	UntypedConstants map[string]bool
	// TypeAliases maps "pkg.AliasName" -> underlying type
	// Go type aliases (type X = Y) are resolved to their underlying type.
	TypeAliases map[string]Type
	// GalaTypeMethods maps "pkg.TypeName" of a GALA type -> the methods
	// hand-written .go files of its package declare on it (Kind
	// GoKindMethodsOnly). It is apart from Types because the two key spaces
	// overlap: a GALA package is keyed by its name, and a Go package of the
	// same name (GALA `fs` beside Go `io/fs`) may declare a type of the same
	// name, so one map would let either record overwrite the other.
	GalaTypeMethods map[string]*GoTypeData
}

// GoFuncSignature describes a Go function's type signature.
type GoFuncSignature struct {
	Params     []GoParam // parameter names + types
	Returns    []Type    // return types (supports multi-return)
	IsVariadic bool      // true if last param is ...T
	// TypeParams holds the declared type-parameter names of a generic Go
	// function, in declaration order (empty for a non-generic function).
	//
	// Params and Returns record the signature exactly as declared, so for
	// `func MapPut[K comparable, V any](m map[K]V, k K, v V) map[K]V` the
	// return is `map[K]V` — a type that only means something once K and V are
	// bound. Without the names there is no way to tell those identifiers apart
	// from ordinary types, so a call site cannot instantiate the signature and
	// would materialize the type parameters verbatim into generated Go.
	TypeParams []string
}

// GoParam describes a single function parameter.
type GoParam struct {
	Name string
	Type Type
}

// GoKindMethodsOnly is the GoTypeData.Kind of a type that hand-written Go only
// declares methods on: the type itself is declared elsewhere in the package (a
// GALA struct, say), so only Methods and PointerMethods are filled in. Such a
// record is filed in GoTypeInfo.GalaTypeMethods, never in Types.
//
// The type's method set is thereby split between its GALA TypeMetadata and
// this record, and a check of "does T have method M" consults both (GALA-E0044,
// Go method-call typing, the addressable-receiver check). Folding these methods
// into TypeMetadata.Methods would cover every reader at once, but would route
// them through GALA method lowering and lose their multi-result signatures, so
// they stay on the Go side.
const GoKindMethodsOnly = "methods-only"

// GoTypeData describes a Go type (struct, interface, or named type).
type GoTypeData struct {
	Kind       string                    // "struct", "interface", "alias", "named", GoKindMethodsOnly
	Fields     map[string]Type           // struct fields (exported only)
	FieldOrder []string                  // exported struct fields in declaration order (parallel keys to Fields)
	Methods    map[string]*GoFuncSignature // method set (exported only)
	Underlying Type                      // underlying type for aliases and named types
	TypeParams []string                  // type parameter names for generic types (empty for non-generic)
	// PointerMethods names the exported methods declared on *T only (in the
	// method set of *T but not of T). Go calls these only on an addressable
	// receiver.
	PointerMethods map[string]bool
	// NoCopy names the type that makes a value of this type unsafe to copy
	// ("sync.Mutex" for a struct holding one), or "" when copying is fine.
	// See noCopyReason in the analyzer.
	NoCopy string
}

// NewGoTypeInfo creates an empty GoTypeInfo.
func NewGoTypeInfo() *GoTypeInfo {
	return &GoTypeInfo{
		Functions:        make(map[string]*GoFuncSignature),
		Types:            make(map[string]*GoTypeData),
		Variables:        make(map[string]Type),
		Constants:        make(map[string]Type),
		UntypedConstants: make(map[string]bool),
		TypeAliases:      make(map[string]Type),
		GalaTypeMethods:  make(map[string]*GoTypeData),
	}
}

// IsEmpty reports whether g declares nothing: no functions, types, variables,
// constants, type aliases or methods on GALA types.
func (g *GoTypeInfo) IsEmpty() bool {
	return len(g.Functions) == 0 && len(g.Types) == 0 && len(g.Variables) == 0 &&
		len(g.Constants) == 0 && len(g.TypeAliases) == 0 && len(g.GalaTypeMethods) == 0
}

// Merge combines another GoTypeInfo into this one.
func (g *GoTypeInfo) Merge(other *GoTypeInfo) {
	if other == nil {
		return
	}
	for k, v := range other.Functions {
		g.Functions[k] = v
	}
	for k, v := range other.Types {
		g.Types[k] = v
	}
	for k, v := range other.Variables {
		g.Variables[k] = v
	}
	for k, v := range other.Constants {
		g.Constants[k] = v
	}
	if len(other.UntypedConstants) > 0 && g.UntypedConstants == nil {
		g.UntypedConstants = make(map[string]bool, len(other.UntypedConstants))
	}
	for k, v := range other.UntypedConstants {
		g.UntypedConstants[k] = v
	}
	for k, v := range other.TypeAliases {
		g.TypeAliases[k] = v
	}
	if len(other.GalaTypeMethods) > 0 && g.GalaTypeMethods == nil {
		g.GalaTypeMethods = make(map[string]*GoTypeData, len(other.GalaTypeMethods))
	}
	for k, v := range other.GalaTypeMethods {
		g.GalaTypeMethods[k] = v
	}
}

// GetGalaTypeMethods returns the methods hand-written .go files declare on the
// GALA type keyed "pkg.TypeName", or nil.
func (g *GoTypeInfo) GetGalaTypeMethods(qualifiedName string) *GoTypeData {
	if g == nil {
		return nil
	}
	return g.GalaTypeMethods[qualifiedName]
}

// GetFuncReturnType returns the first return type of a Go function, or nil if unknown.
// This is the most common case for type inference (single-return functions).
func (g *GoTypeInfo) GetFuncReturnType(qualifiedName string) Type {
	if g == nil {
		return nil
	}
	if sig, ok := g.Functions[qualifiedName]; ok && len(sig.Returns) > 0 {
		return sig.Returns[0]
	}
	return nil
}

// GetFuncSignature returns the full function signature, or nil if unknown.
// AddImportPathKeys records every entry of the package named pkgName again
// under its import path, `importPath.Name` beside `pkgName.Name`. A key by
// name alone cannot tell two packages of one name apart (two `util` packages
// of a module, GALA's `strings` and Go's); a key by import path is exact, and
// a lookup through an import prefers it (see HasQualified).
func (g *GoTypeInfo) AddImportPathKeys(pkgName, importPath string) {
	if g == nil || pkgName == "" || importPath == "" || importPath == pkgName {
		return
	}
	prefix := pkgName + "."
	copyKeys := func(keys []string, add func(from, to string)) {
		for _, k := range keys {
			if rest, ok := strings.CutPrefix(k, prefix); ok {
				add(k, importPath+"."+rest)
			}
		}
	}
	copyKeys(mapKeys(g.Functions), func(from, to string) { g.Functions[to] = g.Functions[from] })
	copyKeys(mapKeys(g.Types), func(from, to string) { g.Types[to] = g.Types[from] })
	copyKeys(mapKeys(g.Variables), func(from, to string) { g.Variables[to] = g.Variables[from] })
	copyKeys(mapKeys(g.Constants), func(from, to string) { g.Constants[to] = g.Constants[from] })
	copyKeys(mapKeys(g.UntypedConstants), func(from, to string) { g.UntypedConstants[to] = g.UntypedConstants[from] })
	copyKeys(mapKeys(g.TypeAliases), func(from, to string) { g.TypeAliases[to] = g.TypeAliases[from] })
	copyKeys(mapKeys(g.GalaTypeMethods), func(from, to string) { g.GalaTypeMethods[to] = g.GalaTypeMethods[from] })
}

// HasQualified reports whether any entry is recorded under key.
func (g *GoTypeInfo) HasQualified(key string) bool {
	if g == nil {
		return false
	}
	if _, ok := g.Functions[key]; ok {
		return true
	}
	if _, ok := g.Types[key]; ok {
		return true
	}
	if _, ok := g.Variables[key]; ok {
		return true
	}
	if _, ok := g.Constants[key]; ok {
		return true
	}
	_, ok := g.TypeAliases[key]
	return ok
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (g *GoTypeInfo) GetFuncSignature(qualifiedName string) *GoFuncSignature {
	if g == nil {
		return nil
	}
	return g.Functions[qualifiedName]
}

// DeclaresType reports whether the Go package filed under pkgName declares a
// type (or type alias) called name.
func (g *GoTypeInfo) DeclaresType(pkgName, name string) bool {
	if g == nil {
		return false
	}
	key := pkgName + "." + name
	_, isType := g.Types[key]
	_, isAlias := g.TypeAliases[key]
	return isType || isAlias
}

// GetTypeData returns type metadata for a Go type, or nil if unknown.
func (g *GoTypeInfo) GetTypeData(qualifiedName string) *GoTypeData {
	if g == nil {
		return nil
	}
	return g.Types[qualifiedName]
}

// GetFieldType returns the type of a struct field, or nil if unknown.
func (g *GoTypeInfo) GetFieldType(typeName, fieldName string) Type {
	if g == nil {
		return nil
	}
	td := g.Types[typeName]
	if td == nil {
		return nil
	}
	return td.Fields[fieldName]
}

// GetMethodSignature returns the full method signature, or nil if unknown.
func (g *GoTypeInfo) GetMethodSignature(typeName, methodName string) *GoFuncSignature {
	if g == nil {
		return nil
	}
	td := g.Types[typeName]
	if td == nil {
		return nil
	}
	if sig, ok := td.Methods[methodName]; ok {
		return sig
	}
	return nil
}

// GetMethodReturnType returns the first return type of a method, or nil if unknown.
func (g *GoTypeInfo) GetMethodReturnType(typeName, methodName string) Type {
	if g == nil {
		return nil
	}
	td := g.Types[typeName]
	if td == nil {
		return nil
	}
	if sig, ok := td.Methods[methodName]; ok && len(sig.Returns) > 0 {
		return sig.Returns[0]
	}
	return nil
}

// ResolveTypeAlias returns the underlying type if the given name is a Go type alias,
// or nil if it's not an alias.
func (g *GoTypeInfo) ResolveTypeAlias(qualifiedName string) Type {
	if g == nil {
		return nil
	}
	return g.TypeAliases[qualifiedName]
}
