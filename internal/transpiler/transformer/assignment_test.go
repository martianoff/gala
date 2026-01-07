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

func TestAssignment(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, []string{"."})
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{
			name: "assign to var",
			input: `package main

func main() {
    var x = 10
    x = 20
}`,
			expected: `package main

func main() {
	var x = 10
	x = 20
}`,
		},
		{
			name: "assign to val (should fail)",
			input: `package main

func main() {
    val x = 10
    x = 20
}`,
			expectError: true,
		},
		{
			name: "val declaration without value (should fail)",
			input: `package main

val x int`,
			expectError: true,
		},
		{
			name: "var declaration without value",
			input: `package main

var x int`,
			expectError: false,
			expected: `package main

var x int`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
			}
		})
	}
}
