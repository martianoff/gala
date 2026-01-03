package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStructs(t *testing.T) {
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
			name: "Simple struct",
			input: `type Person struct {
				Name string
				Age int
			}`,
			expected: `package main

type Person struct {
	Name string
	Age  int
}
`,
		},
		{
			name: "Struct with val and var fields",
			input: `type Config struct {
				val ID string
				var Count int
			}`,
			expected: `package main

type Config struct {
	ID    string
	Count int
}
`,
		},
		{
			name: "Struct with tags",
			input: `type User struct {
				Name string "json:\"name\""
			}`,
			expected: `package main

type User struct {
	Name string "json:\"name\""
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
