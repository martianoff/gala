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

// TestTuplePositionalConstructor verifies that the bare-positional Tuple
// constructor (no explicit type args, no named-arg form) emits a properly
// instantiated Go composite literal.
//
// Regression: previously `Tuple(true, "1")` emitted `std.Tuple{V1: ..., V2: ...}`
// (no type-arg list), which Go rejects with "cannot use generic type without
// instantiation". `Tuple(1, "x", 3.5)` (3 args) emitted `std.Tuple{V1, V2}`,
// silently dropping the third arg because the Tuple→TupleN arity rewrite
// was only applied at the literal-tuple `(a, b, c)` site.
func TestTuplePositionalConstructor(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		expected    []string
		notExpected []string
	}{
		{
			name: "positional 2-tuple infers type args",
			input: `package main

func main() {
    val pair = Tuple(true, "1")
}`,
			expected: []string{
				"std.Tuple[bool, string]{",
				"V1: std.NewImmutable(true)",
				"V2: std.NewImmutable(\"1\")",
			},
			notExpected: []string{
				// the broken form must not appear: bare `std.Tuple{` without
				// type-arg brackets is rejected by Go.
				"std.Tuple{V1",
			},
		},
		{
			name: "positional 3-tuple uses Tuple3 with inferred type args",
			input: `package main

func main() {
    val triple = Tuple(1, "x", 3.5)
}`,
			expected: []string{
				"std.Tuple3[int, string, float64]{",
				"V1: std.NewImmutable(1)",
				"V2: std.NewImmutable(\"x\")",
				"V3: std.NewImmutable(3.5)",
			},
			notExpected: []string{
				// the broken form: 3-arg call routed to 2-field Tuple, V3 dropped.
				"std.Tuple{V1",
				"std.Tuple[int",
			},
		},
		{
			name: "positional 4-tuple uses Tuple4",
			input: `package main

func main() {
    val q = Tuple(1, 2, 3, 4)
}`,
			expected: []string{
				"std.Tuple4[int, int, int, int]{",
				"V1: std.NewImmutable(1)",
				"V4: std.NewImmutable(4)",
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
			for _, ne := range tt.notExpected {
				assert.False(t, strings.Contains(got, ne), "Output should NOT contain %q\nGot:\n%s", ne, got)
			}
		})
	}
}
