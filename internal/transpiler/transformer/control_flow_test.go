package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestControlFlow(t *testing.T) {
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
			name: "Match expression",
			input: `val res = x match {
				case 1 => "one"
				case 2 => "two"
				case _ => "many"
			}`,
			expected: `package main

import "martianoff/gala/std"

var res = std.NewImmutable(func(x any) any {
	switch x {
	case 1:
		return "one"
	case 2:
		return "two"
	default:
		return "many"
	}
	return nil
}(x))
`,
		},
		{
			name: "Match expression with shadowing",
			input: `val x = 10
val res = x match {
	case 10 => x
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
var res = std.NewImmutable(func(x any) any {
	switch x {
	case 10:
		return x
	}
	return nil
}(x.Get()))
`,
		},
		{
			name:  "Top-level expression",
			input: `println("hello")`,
			expected: `package main

func init() {
	println("hello")
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
