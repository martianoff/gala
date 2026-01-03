package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEqualMethod(t *testing.T) {
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
			name: "Struct with basic fields",
			input: `package main

struct Point(x int, y int)`,
			expected: `package main

import "martianoff/gala/std"

type Point struct {
	x std.Immutable[int]
	y std.Immutable[int]
}

func (s Point) Copy() Point {
	return Point{x: std.Copy(s.x), y: std.Copy(s.y)}
}
func (s Point) Equal(other Point) bool {
	return std.Equal(s.x, other.x) && std.Equal(s.y, other.y)
}
`,
		},
		{
			name: "Empty struct",
			input: `package main

struct Empty()`,
			expected: `package main

type Empty struct {
}

func (s Empty) Copy() Empty {
	return Empty{}
}
func (s Empty) Equal(other Empty) bool {
	return true
}
`,
		},
		{
			name: "Struct with mixed val and var fields",
			input: `package main

type Mixed struct {
	val Name string
	var Age  int
}`,
			expected: `package main

import "martianoff/gala/std"

type Mixed struct {
	Name std.Immutable[string]
	Age  int
}

func (s Mixed) Copy() Mixed {
	return Mixed{Name: std.Copy(s.Name), Age: std.Copy(s.Age)}
}
func (s Mixed) Equal(other Mixed) bool {
	return std.Equal(s.Name, other.Name) && std.Equal(s.Age, other.Age)
}
`,
		},
		{
			name: "Struct with only var fields",
			input: `package main

type Mutable struct {
	var X int
	var Y int
}`,
			expected: `package main

import "martianoff/gala/std"

type Mutable struct {
	X int
	Y int
}

func (s Mutable) Copy() Mutable {
	return Mutable{X: std.Copy(s.X), Y: std.Copy(s.Y)}
}
func (s Mutable) Equal(other Mutable) bool {
	return std.Equal(s.X, other.X) && std.Equal(s.Y, other.Y)
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
