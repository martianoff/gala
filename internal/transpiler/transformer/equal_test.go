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
	x int
	y int
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

import "martianoff/gala/std"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
