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

// TestTupleReturnArityRewrite verifies that the user-visible name `Tuple`
// with 3+ type arguments is rewritten to the arity-specific TupleN across
// every code path that builds a Go type expression from a transpiler.GenericType
// — not just the source-level transformType path.
//
// Regression: when a function's return type was Option[Tuple[int, int, int]]
// and the value flowed into a `match`, the IIFE parameter type was rebuilt
// via typeToExpr and emitted as `Option[Tuple[int, int, int]]` (2-arity Tuple
// with 3 type args) — Go rejected the malformed instantiation. Meanwhile the
// signature itself emitted Tuple3 correctly because that path already had
// the rewrite. The two halves disagreed.
func TestTupleReturnArityRewrite(t *testing.T) {
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
			name: "match IIFE parameter type uses TupleN arity",
			input: `package main

func wrap() Option[Tuple[int, int, int]] = Some((1, 2, 3))

func main() {
    wrap() match {
        case Some(_) => "ok"
        case _       => "none"
    }
}`,
			expected: []string{
				"func wrap() std.Option[std.Tuple3[int, int, int]]",
				"func(obj std.Option[std.Tuple3[int, int, int]])",
				"std.Some[std.Tuple3[int, int, int]]{}.Unapply",
				"var _tmp_3 std.Tuple3[int, int, int]",
			},
			notExpected: []string{
				// the broken form: 2-arity Tuple with 3 type args
				"std.Tuple[int, int, int]",
				"std.Option[std.Tuple[int, int, int]]",
				"std.Some[std.Tuple[int, int, int]]",
			},
		},
		{
			name: "4-arity also rewritten in match IIFE",
			input: `package main

func wrap() Option[Tuple[int, int, int, int]] = Some((1, 2, 3, 4))

func main() {
    wrap() match {
        case Some(_) => "ok"
        case _       => "none"
    }
}`,
			expected: []string{
				"std.Option[std.Tuple4[int, int, int, int]]",
				"func(obj std.Option[std.Tuple4[int, int, int, int]])",
			},
			notExpected: []string{
				"std.Tuple[int, int, int, int]",
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
