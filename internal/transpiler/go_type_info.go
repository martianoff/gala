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

// GoTypeData describes a Go type (struct, interface, or named type).
type GoTypeData struct {
	Kind       string                    // "struct", "interface", "alias", "named"
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
	}
}

// IsEmpty reports whether g declares nothing: no functions, types, variables,
// constants or type aliases.
func (g *GoTypeInfo) IsEmpty() bool {
	return len(g.Functions) == 0 && len(g.Types) == 0 && len(g.Variables) == 0 &&
		len(g.Constants) == 0 && len(g.TypeAliases) == 0
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

// DeclaredTypeNames returns the bare names of the types and type aliases g
// files under pkgName.
func (g *GoTypeInfo) DeclaredTypeNames(pkgName string) map[string]bool {
	names := map[string]bool{}
	if g == nil {
		return names
	}
	prefix := pkgName + "."
	for key := range g.Types {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			names[name] = true
		}
	}
	for key := range g.TypeAliases {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			names[name] = true
		}
	}
	return names
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
