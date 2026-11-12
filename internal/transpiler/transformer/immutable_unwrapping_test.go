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
	// Module root will be found automatically by looking for go.mod
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

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
    Println(y)
}`,
			expected: `package main

import "fmt"

func main() {
	var x = 1
	var y = x
	fmt.Println(y)
}`,
		},
		{
			name: "assign immutable val to val",
			input: `package main

func main() {
    val x = 1
    val z = x
    Println(z)
}`,
			expected: `package main

import "fmt"

func main() {
	var x = 1
	var z = x
	fmt.Println(z)
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

func foo(x int) int {
	var y = 1
	return y
}`,
		},
		{
			// A package-level val stays boxed: every read unwraps it.
			name: "assign package-level val to local val and var",
			input: `package main

val x = 1

func main() {
    val z = x
    Println(z)
    var y = x
    Println(y)
}`,
			expected: `package main

import "fmt"
import "martianoff/gala/std"

var x = std.NewImmutable(1)

func main() {
	var z = x.Get()
	fmt.Println(z)
	var y = x.Get()
	fmt.Println(y)
}`,
		},
		{
			name: "assign from function returning Immutable",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)

func main() {
    var y = getImm()
    Println(y)
}`,
			expected: `package main

import "fmt"
import "martianoff/gala/std"

func getImm() std.Immutable[int] {
	return std.NewImmutable(1)
}
func main() {
	var y = getImm().Get()
	fmt.Println(y)
}`,
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
