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

func TestMultiVariables(t *testing.T) {
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
			name: "multi val declaration",
			input: `package main

val a, b = 1, 2`,
			expected: `package main

import "martianoff/gala/std"

var a, b = std.NewImmutable(1), std.NewImmutable(2)
`,
		},
		{
			name: "multi var declaration",
			input: `package main

var x, y int = 10, 20`,
			expected: `package main

var x, y int = 10, 20
`,
		},
		{
			name: "short var decl is immutable",
			input: `package main

func main() {
    z := 30
    // z = 40 // this would be an error if checked by transpiler
}`,
			expected: `package main

import "martianoff/gala/std"

func main() {
	z := std.NewImmutable(30)
}
`,
		},
		{
			name: "multi short var decl is immutable",
			input: `package main

func main() {
    a, b := 1, 2
}`,
			expected: `package main

import "martianoff/gala/std"

func main() {
	a, b := std.NewImmutable(1), std.NewImmutable(2)
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(stripGeneratedHeader(got)))
		})
	}
}

func TestImmutableAssignmentError(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

func main() {
    x := 10
    x = 20
}`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot assign to immutable variable x")
}
