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

func TestDefaultImmutability(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, []string{"."})
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Static field immutable by default",
			input: `package main

type Person struct {
	Name string
}`,
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
`,
		},
		{
			name: "Generic field immutable by default",
			input: `package main

type Box[T any] struct {
	Value T
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
`,
		},
		{
			name: "Explicit var field stays mutable",
			input: `package main

type Counter struct {
	var Count int
}`,
			expected: `package main

import "martianoff/gala/std"

type Counter struct {
	Count int
}

func (s Counter) Copy() Counter {
	return Counter{Count: std.Copy(s.Count)}
}
func (s Counter) Equal(other Counter) bool {
	return std.Equal(s.Count, other.Count)
}
func (s Counter) Unapply(v any) (int, bool) {
	if p, ok := v.(Counter); ok {
		return p.Count, true
	}
	if p, ok := v.(*Counter); ok && p != nil {
		return p.Count, true
	}
	return *new(int), false
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
