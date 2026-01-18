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

func TestVariables(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "val declaration",
			input: `package main

val x = 10`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
`,
		},
		{
			name: "var declaration",
			input: `package main

var y = 20`,
			expected: `package main

var y = 20
`,
		},
		{
			name: "val with type",
			input: `package main

val s string = "hello"`,
			expected: `package main

import "martianoff/gala/std"

var s std.Immutable[string] = std.NewImmutable[string]("hello")
`,
		},
		{
			name: "val inferred from literal and used in var",
			input: `package main

val x = 10
var y = x`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
var y = x.Get()
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
