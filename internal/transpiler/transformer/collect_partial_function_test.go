package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

// TestPartialFunctionTuplePatternBindings covers the fix for: a partial
// function literal with a tuple-destructuring case like
//   { case (true, code) => code }
// must bind EVERY element of the tuple pattern, not just the first one
// that contributes a non-trivial condition. The transpiler used to short
// circuit at the first literal-equality match and skip later identifier
// bindings, so `code` was emitted as an undefined identifier.
func TestPartialFunctionTuplePatternBindings(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := newCheckedTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		mustContain []string
		mustNotHave []string
	}{
		{
			name: "Collect with tuple destructuring binds the second element",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val pairs = ArrayOf[Tuple[bool, string]]((true, "a"), (false, "b"), (true, "c"))
    val onlyA = pairs.Collect({ case (true, code) => code })
    Println(onlyA.String())
}`,
			mustContain: []string{
				"code := _pf_arg.V2.Get()",            // V2 binding emitted
				"_pf_arg.V1.Get() == true",            // V1 condition still emitted
				"Some[string]{}.Apply(code)",          // wrapped result references binding
				"Option[string]",                       // result type concrete
			},
			mustNotHave: []string{
				"Option[void]",                         // void must not leak
				"Some[any]{}.Apply(code)",              // any-fallback must not leak
			},
		},
		{
			name: "Collect whose result is the enclosing type parameter keeps it",
			input: `package main

import . "martianoff/gala/collection_immutable"

func keepPositive[T any](xs Array[T], key func(T) int) Array[T] =
    xs.Collect({ case x if key(x) > 0 => x })

func main() {
    Println(keepPositive(ArrayOf(1, -2, 3), (i) => i))
}`,
			mustContain: []string{
				"func(_pf_arg T) std.Option[T] {",
				"std.None[T]{}.Apply()",
			},
			mustNotHave: []string{
				"Option[any]",
				"None[any]",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			out := stripGeneratedHeader(got)
			for _, s := range tt.mustContain {
				if !strings.Contains(out, s) {
					t.Errorf("expected output to contain %q\n--- got ---\n%s", s, out)
				}
			}
			for _, s := range tt.mustNotHave {
				if strings.Contains(out, s) {
					t.Errorf("expected output NOT to contain %q\n--- got ---\n%s", s, out)
				}
			}
		})
	}
}
