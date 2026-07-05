package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A generic free function whose type parameter appears ONLY in its return type
// (a "phantom" param) cannot have that param inferred by Go from the call's
// arguments. The transpiler must therefore emit explicit type args resolved
// from the expected type — the enclosing function's return type, or the pushed
// call-site hint — instead of an uninstantiated `Fn(args)` that fails to
// compile in Go with "cannot infer <param>".
//
// Regression: `func InvalidOf[E, A any](err E) Validated[E, A]` used from a
// function returning `Validated[string, string]` emitted a bare
// `InvalidOf("x")` (E inferable from the arg, A a phantom Go could not infer),
// producing invalid Go. It now emits `InvalidOf[string, string]("x")`.
func TestFuncPhantomReturnTypeParamInference(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// A two-param generic where the second param is carried only by the return
	// type of `failWith`, exactly like Validated's Invalid-only constructor.
	decl := `package main

type Result[E any, A any] struct {
    var tag E
    var ok bool
}

func failWith[E any, A any](e E) Result[E, A] = Result[E, A]{tag: e, ok: false}

// idf's single param is fully determined by its argument — Go infers it, so we
// must NOT inject explicit type args here (that would change working output).
func idf[T any](x T) T = x
`

	tests := []struct {
		name        string
		input       string
		contains    []string
		notContains []string
	}{
		{
			// Phantom A resolved from the enclosing function's return type.
			name: "phantom param filled from enclosing return type",
			input: decl + `
func run() Result[string, int] = failWith("boom")
`,
			contains:    []string{`failWith[string, int]("boom")`},
			notContains: []string{`failWith("boom")`},
		},
		{
			// Non-phantom function whose param is arg-derivable stays verbatim;
			// Go infers T, so no explicit type args are injected.
			name: "arg-derivable param is left to Go inference",
			input: decl + `
func useId() int = idf(5)
`,
			contains:    []string{"idf(5)"},
			notContains: []string{"idf[int]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want, "generated Go must contain %q", want)
			}
			for _, bad := range tt.notContains {
				assert.NotContains(t, got, bad, "generated Go must not contain %q", bad)
			}
		})
	}
}
