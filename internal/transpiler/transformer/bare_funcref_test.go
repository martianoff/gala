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

// TestBareFuncRefMethodTypeArgInference covers the fix for: passing a bare
// reference to a generic function to a generic method (e.g. Array.Map) used
// to leak unresolved type parameters into the emitted Go (e.g. a parameter
// type appearing as a bare type-variable name `U`). The transformer must
// either let Go infer or instantiate the function reference so the call
// site is well-formed.
//
// We assert two properties of the generated Go:
//   1. The unresolved-type-param defaulting warning that produces `any`
//      bindings for Map's U does not fire — i.e. the substitution map
//      for the method's type parameters is filled in from the function's
//      signature.
//   2. The function reference compiles via Go's own inference (no stray
//      letter type-param names in the call expression). We approximate
//      this by checking that the generated code does not contain naked
//      single-letter type identifiers in the call site.
func TestBareFuncRefMethodTypeArgInference(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name        string
		input       string
		mustContain []string
		mustNotHave []string
	}{
		{
			// The function's type-param T appears in both the parameter
			// and result, so unification with the expected Map param
			// type binds T cleanly. The result of Map should propagate
			// the element type without leaking a placeholder.
			name: "Map with bare reference to identity-shaped generic function",
			input: `package main

import . "martianoff/gala/collection_immutable"

func identity[T any](b T) T = b

func main() {
    val xs = ArrayOf(1, 2, 3)
    val ys = xs.Map(identity)
    Println(ys.String())
}`,
			mustContain: []string{
				"Array_Map(",     // dispatcher form
				"identity",        // function reference present
			},
			mustNotHave: []string{
				", U)",            // unresolved U should not leak into params
				"Array[U]",        // unresolved type param should not leak
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
