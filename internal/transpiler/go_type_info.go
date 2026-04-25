package transpiler

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
	// Constants maps "pkg.ConstName" -> type
	Constants map[string]Type
	// TypeAliases maps "pkg.AliasName" -> underlying type
	// Go type aliases (type X = Y) are resolved to their underlying type.
	TypeAliases map[string]Type
}

// GoFuncSignature describes a Go function's type signature.
type GoFuncSignature struct {
	Params     []GoParam // parameter names + types
	Returns    []Type    // return types (supports multi-return)
	IsVariadic bool      // true if last param is ...T
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
	Methods    map[string]*GoFuncSignature // method set (exported only)
	Underlying Type                      // underlying type for aliases and named types
	TypeParams []string                  // type parameter names for generic types (empty for non-generic)
}

// NewGoTypeInfo creates an empty GoTypeInfo.
func NewGoTypeInfo() *GoTypeInfo {
	return &GoTypeInfo{
		Functions:   make(map[string]*GoFuncSignature),
		Types:       make(map[string]*GoTypeData),
		Variables:   make(map[string]Type),
		Constants:   make(map[string]Type),
		TypeAliases: make(map[string]Type),
	}
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
