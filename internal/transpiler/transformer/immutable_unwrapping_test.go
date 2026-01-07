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

func TestImmutableUnwrapping(t *testing.T) {
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
			name: "assign immutable val to var",
			input: `package main

func main() {
    val x = 1
    var y = x
}`,
			expected: `package main

import "martianoff/gala/std"

func main() {
	var x = std.NewImmutable(1)
	var y = x.Get()
}`,
		},
		{
			name: "assign immutable val to val",
			input: `package main

func main() {
    val x = 1
    val z = x
}`,
			expected: `package main

import "martianoff/gala/std"

func main() {
	var x = std.NewImmutable(1)
	var z = std.NewImmutable(x.Get())
}`,
		},
		{
			name: "return immutable from func",
			input: `package main

func foo(x int) int {
    val y = 1
    return y
}`,
			expected: `package main

import "martianoff/gala/std"

func foo(x int) int {
	var y = std.NewImmutable(1)
	return y.Get()
}`,
		},
		{
			name: "assign from function returning Immutable",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)

func main() {
    var y = getImm()
}`,
			expected: `package main

import "martianoff/gala/std"

func getImm() std.Immutable[int] {
	return std.NewImmutable(1)
}
func main() {
	var y = getImm().Get()
}`,
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
