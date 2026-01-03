package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFunctions(t *testing.T) {
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
			name:  "Standard Go-style function",
			input: `func add(a int, b int) int { return a + b }`,
			expected: `package main

func add(a int, b int) int {
	return a + b
}
`,
		},
		{
			name:  "Scala-style shorthand function",
			input: `func square(x int) int = x * x`,
			expected: `package main

func square(x int) int {
	return x * x
}
`,
		},
		{
			name:  "Lambda expression",
			input: `val f = (x int) => x * x`,
			expected: `package main

var f = func(x int) {
	return x * x
}
`,
		},
		{
			name:  "If expression",
			input: `val res = if (c) 1 else 2`,
			expected: `package main

var res = func() any {
	if c {
		return 1
	}
	return 2
}()
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
