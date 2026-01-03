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
`,
		},
		{
			name: "Explicit var field stays mutable",
			input: `package main
type Counter struct {
	var Count int
}`,
			expected: `package main

type Counter struct {
	Count int
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
