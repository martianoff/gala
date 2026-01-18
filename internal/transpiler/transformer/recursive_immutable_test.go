package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecursiveImmutableError(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name  string
		input string
	}{
		{
			name: "Explicit recursive Immutable in val",
			input: `package main

func main() {
    val x Immutable[int] = NewImmutable(1)
}`,
		},
		{
			name: "Nested NewImmutable",
			input: `package main

func main() {
    val x = NewImmutable(NewImmutable(1))
}`,
		},
		{
			name: "Function returning Immutable wrapped in NewImmutable",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    val x = NewImmutable(getImm())
}`,
		},
		{
			name: "Explicit recursive Immutable in var",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    var x Immutable[Immutable[int]] = getImm()
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "recursive Immutable wrapping")
		})
	}
}
