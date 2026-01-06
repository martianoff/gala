package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMethods(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Basic method",
			input: `package main

type Person struct { Name string }
func (p Person) Greet() string = "Hello, " + p.Name`,
			expected: `package main

import "martianoff/gala/std"

type Person struct {
	Name std.Immutable[string]
}

func (s Person) Copy() Person {
	return Person{Name: std.Copy(s.Name)}
}
func (s Person) Equal(other Person) bool {
	return std.Equal(s.Name, other.Name)
}
func (s Person) Unapply(v any) (std.Immutable[string], bool) {
	if p, ok := v.(Person); ok {
		return p.Name, true
	}
	if p, ok := v.(*Person); ok && p != nil {
		return p.Name, true
	}
	return *new(std.Immutable[string]), false
}
func (p Person) Greet() string {
	return "Hello, " + p.Name.Get()
}
`,
		},
		{
			name: "Method with type parameters",
			input: `package main

type Box[T any] struct { Value T }
func (b Box[T]) MyMap[U any](f func(T) U) Box[U] = Box(f(b.Value))`,
			expected: `package main

import "martianoff/gala/std"

type Box[T any] struct {
	Value std.Immutable[T]
}

func (s Box[T]) Copy() Box[T] {
	return Box[T]{Value: std.Copy(s.Value)}
}
func (s Box[T]) Equal(other Box[T]) bool {
	return std.Equal(s.Value, other.Value)
}
func (s Box[T]) Unapply(v any) (std.Immutable[T], bool) {
	if p, ok := v.(Box[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Box[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(std.Immutable[T]), false
}

type BoxInterface interface {
	Get_Value() any
}

func (r Box[T]) Get_Value() any {
	return r.Value.Get()
}
func Box_MyMap[U any, T any](b Box[T], f func(T)U) Box[U] {
	return Box{Value: std.NewImmutable(f(b.Value.Get()))}
}
`,
		},
		{
			name: "Call method with type parameters",
			input: `package main

type Box[T any] struct { Value T }
func (b Box[T]) MyMap[U any](f func(T) U) Box[U] = Box(f(b.Value))
func main() {
    val b = Box(1)
    val b2 = b.MyMap[string]((i int) => "res")
}`,
			expected: `package main

import "martianoff/gala/std"

type Box[T any] struct {
	Value std.Immutable[T]
}

func (s Box[T]) Copy() Box[T] {
	return Box[T]{Value: std.Copy(s.Value)}
}
func (s Box[T]) Equal(other Box[T]) bool {
	return std.Equal(s.Value, other.Value)
}
func (s Box[T]) Unapply(v any) (std.Immutable[T], bool) {
	if p, ok := v.(Box[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Box[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(std.Immutable[T]), false
}

type BoxInterface interface {
	Get_Value() any
}

func (r Box[T]) Get_Value() any {
	return r.Value.Get()
}
func Box_MyMap[U any, T any](b Box[T], f func(T)U) Box[U] {
	return Box{Value: std.NewImmutable(f(b.Value.Get()))}
}
func main() {
	var b = std.NewImmutable(Box{Value: std.NewImmutable(1)})
	var b2 = std.NewImmutable(Box_MyMap[string](b.Get(), func(i int) any {
		return "res"
	}))
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			if err != nil {
				t.Fatalf("Transpile failed: %v", err)
			}
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
