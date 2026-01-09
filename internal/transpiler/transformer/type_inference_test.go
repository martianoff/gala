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

func TestTypeInference(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := []string{"../../.."}
	base := analyzer.GetBaseMetadata(p, searchPaths)
	a := analyzer.NewGalaAnalyzerWithBase(base, p, searchPaths)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "ParenExpr unwrapping",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    var y = (getImm())
}`,
			expected: []string{
				"var y = (getImm().Get())",
			},
		},
		{
			name: "UnaryExpr logical not",
			input: `package main

func getImm() Immutable[bool] = NewImmutable(true)
func main() {
    var y = !getImm()
}`,
			expected: []string{
				"var y = !getImm().Get()",
			},
		},
		{
			name: "BinaryExpr arithmetic",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    var y = getImm() + 1
}`,
			expected: []string{
				"var y = getImm().Get() + 1",
			},
		},
		{
			name: "BinaryExpr comparison",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    var y = getImm() == 1
}`,
			expected: []string{
				"var y = getImm().Get() == 1",
			},
		},
		{
			name: "Complex BinaryExpr",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    var y = (getImm() + 2) * 3
}`,
			expected: []string{
				"var y = (getImm().Get() + 2) * 3",
			},
		},
		{
			name: "Pointer operations",
			input: `package main

func main() {
    val x = 1
    var y = &x
    var z = *y
}`,
			expected: []string{
				"var y = &x",
				"var z = *y",
			},
		},
		{
			name: "Nested ParenExpr",
			input: `package main

func getImm() Immutable[int] = NewImmutable(1)
func main() {
    var y = ((getImm()))
}`,
			expected: []string{
				"var y = (getImm().Get())",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			for _, exp := range tt.expected {
				assert.True(t, strings.Contains(got, exp), "Output missing %q\nGot:\n%s", exp, got)
			}
		})
	}
}
