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

func TestImmutable(t *testing.T) {
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
			name: "val variable and usage",
			input: `package main

val x = 10
val y = x + 1
`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
var y = std.NewImmutable(x.Get() + 1)
`,
		},
		{
			name: "val parameter usage",
			input: `package main

func f(val x int) int = x + 1`,
			expected: `package main

import "martianoff/gala/std"

func f(x std.Immutable[int]) int {
	return x.Get() + 1
}
`,
		},
		{
			name: "val struct field and usage",
			input: `package main

type Config struct {
	val ID string
}
func getID(c Config) string = c.ID`,
			expected: `package main

import "martianoff/gala/std"

type Config struct {
	ID std.Immutable[string]
}

func (s Config) Copy() Config {
	return Config{ID: std.Copy(s.ID)}
}
func (s Config) Equal(other Config) bool {
	return std.Equal(s.ID, other.ID)
}
func (s Config) Unapply(v any) (std.Immutable[string], bool) {
	if p, ok := v.(Config); ok {
		return p.ID, true
	}
	if p, ok := v.(*Config); ok && p != nil {
		return p.ID, true
	}
	return *new(std.Immutable[string]), false
}
func getID(c Config) string {
	return c.ID.Get()
}
`,
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
