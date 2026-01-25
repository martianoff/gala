package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

func TestLiteralRestrictions(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name         string
		input        string
		expectError  bool
		errorContain string
	}{
		{
			name: "slice literal should fail",
			input: `package main

func main() {
    val x = []int{1, 2, 3}
}`,
			expectError:  true,
			errorContain: "slice literals are not supported",
		},
		{
			name: "map literal should fail",
			input: `package main

func main() {
    val x = map[string]int{"a": 1, "b": 2}
}`,
			expectError:  true,
			errorContain: "map literals are not supported",
		},
		{
			name: "empty map literal should fail",
			input: `package main

func main() {
    val x = map[string]int{}
}`,
			expectError:  true,
			errorContain: "map literals are not supported",
		},
		{
			name: "map type in function signature is allowed",
			input: `package main

func process(m map[string]int) map[string]int {
    return m
}`,
			expectError: false,
		},
		{
			name: "map type in variable declaration is allowed",
			input: `package main

var m map[string]int`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContain != "" && err != nil {
					assert.Contains(t, err.Error(), tt.errorContain)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
