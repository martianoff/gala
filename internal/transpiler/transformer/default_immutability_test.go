package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultImmutability(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, tr, g)

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
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
