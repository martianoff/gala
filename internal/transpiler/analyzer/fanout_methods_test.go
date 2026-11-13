package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

func TestMarkFanOutMethods(t *testing.T) {
	tParam := transpiler.BasicType{Name: "T"}
	named := func(pkg, name string) transpiler.NamedType { return transpiler.NamedType{Package: pkg, Name: name} }
	generic := func(pkg, name string, params ...transpiler.Type) transpiler.GenericType {
		return transpiler.GenericType{Base: named(pkg, name), Params: params}
	}
	method := func(name string, ret transpiler.Type) *transpiler.MethodMetadata {
		return &transpiler.MethodMetadata{Name: name, ReturnType: ret}
	}
	box := &transpiler.TypeMetadata{Name: "Box", Package: "p", TypeParams: []string{"T"}, Methods: map[string]*transpiler.MethodMetadata{
		"ToList":   method("ToList", generic("q", "List", tParam)),
		"Same":     method("Same", generic("p", "Box", tParam)),
		"Size":     method("Size", transpiler.BasicType{Name: "int"}),
		"Head":     method("Head", generic("q", "List", tParam)),
		"String":   method("String", generic("q", "List", tParam)),
		"Callback": {Name: "Callback", ParamTypes: []transpiler.Type{transpiler.FuncType{Results: []transpiler.Type{generic("q", "List", tParam)}}}},
		"Nameless": method("Nameless", generic("q", "Marker", tParam)),
	}}
	plain := &transpiler.TypeMetadata{Name: "Plain", Package: "p", Methods: map[string]*transpiler.MethodMetadata{
		"ToList": method("ToList", generic("q", "List", tParam)),
	}}
	richAST := &transpiler.RichAST{Types: map[string]*transpiler.TypeMetadata{
		"p.Box":   box,
		"p.Plain": plain,
		"q.List":  {Name: "List", Package: "q", TypeParams: []string{"T"}, Methods: map[string]*transpiler.MethodMetadata{"Size": method("Size", nil)}},
		"q.Marker": {Name: "Marker", Package: "q", TypeParams: []string{"T"}, Methods: map[string]*transpiler.MethodMetadata{}},
		"q.Headed": {Name: "Headed", Package: "q", IsInterface: true, Methods: map[string]*transpiler.MethodMetadata{"Head": method("Head", nil)}},
		// Only the package's tests see it, so the library decides without it.
		"p.listed": {Name: "listed", Package: "p", IsInterface: true, DefinedIn: "p/box_test.gala", Methods: map[string]*transpiler.MethodMetadata{"ToList": method("ToList", nil)}},
	}}

	markFanOutMethods(richAST, "p")

	lowered := map[string]bool{}
	for name, m := range box.Methods {
		lowered[name] = m.IsGeneric
	}
	assert.Equal(t, map[string]bool{
		"ToList":   true,  // names List[T]
		"Callback": true,  // names List[T] inside a func type
		"Same":     false, // names only its own receiver type
		"Size":     false, // names no generic type
		"Head":     false, // an interface the package sees has Head
		"String":   false, // a Go protocol method
		"Nameless": false, // Marker[T] has no methods to instantiate
	}, lowered)
	assert.False(t, plain.Methods["ToList"].IsGeneric, "a non-generic type's methods are compiled once")
}
