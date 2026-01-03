package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStructMethods(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, tr, g)

	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name: "basic struct methods",
			input: `package main
type Point struct {
    X int
    Y int
}`,
			contains: []string{
				`func (s Point) Copy() Point {`,
				`func (s Point) Equal(other Point) bool {`,
				`return Point{X: std.Copy(s.X), Y: std.Copy(s.Y)}`,
				`return std.Equal(s.X, other.X) && std.Equal(s.Y, other.Y)`,
			},
		},
		{
			name: "generic struct methods",
			input: `package main
type Box[T any] struct {
    Value T
}`,
			contains: []string{
				`func (s Box[T]) Copy() Box[T] {`,
				`func (s Box[T]) Equal(other Box[T]) bool {`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			assert.NoError(t, err)
			for _, c := range tt.contains {
				assert.Contains(t, got, c)
			}
		})
	}
}
