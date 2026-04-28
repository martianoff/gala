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

// TestTupleLiteralCallSiteInference verifies bidirectional type inference for
// tuple literals used as call arguments. The tuple element types must be
// derived from the call site's expected parameter type, not collapse to
// `Tuple[any, ...]` when individual element-expression types alone would
// resolve correctly.
//
// Regression: previously `arr.Append((name, err))` where the call expects
// `Tuple[string, error]` emitted `std.Tuple[any, error]{...}`, which Go
// rejected with "cannot use Tuple[any, error] as Tuple[string, error]".
func TestTupleLiteralCallSiteInference(t *testing.T) {
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
			// Direct repro: a function with parameter type Tuple[string, error]
			// receives a tuple literal whose first element is a typed string
			// and whose second element is a typed error. Without bidirectional
			// inference the tuple literal collapses to Tuple[any, error].
			name: "tuple literal as call arg uses expected param type",
			input: `package main

import "errors"

func consume(p Tuple[string, error]) string = p.V1

func main() {
    val name = "x"
    val err = errors.New("boom")
    val s = consume((name, err))
}`,
			expected: []string{
				"std.Tuple[string, error]",
			},
			notExpected: []string{
				"std.Tuple[any, error]",
				"std.Tuple[any, any]",
			},
		},
		{
			// Method-call shape mirroring the bug exactly: an Array.Append
			// call where the array's element type is Tuple[string, error]
			// and the literal supplies the pair.
			name: "Array.Append((name, err)) keeps Tuple[string, error]",
			input: `package main

import (
    "errors"
    . "martianoff/gala/collection_immutable"
)

func collect() Array[Tuple[string, error]] {
    var arr = EmptyArray[Tuple[string, error]]()
    val name = "x"
    val err = errors.New("boom")
    arr = arr.Append((name, err))
    return arr
}`,
			expected: []string{
				"std.Tuple[string, error]",
			},
			notExpected: []string{
				"std.Tuple[any, error]",
			},
		},
		{
			// Negative — make sure the no-call-context path still infers from
			// element expression types. A bare val-binding has no expected
			// type so the existing element-by-element inference must remain
			// the source of truth.
			name: "val-bound tuple literal still infers from element types",
			input: `package main

import "errors"

func main() {
    val name = "x"
    val err = errors.New("boom")
    val pair = (name, err)
}`,
			expected: []string{
				"std.Tuple[string, error]",
			},
			notExpected: []string{
				"std.Tuple[any, error]",
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
