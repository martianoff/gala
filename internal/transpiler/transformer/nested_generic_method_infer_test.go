package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A generic method's result type parameter must be inferred from the RETURN
// type of a lambda argument even when the method's own type parameter shares
// its source name with one of the receiver's type arguments — a situation that
// arises whenever the call sits inside a generic function whose type params
// reuse the same letters as the receiver type's declared params.
//
// Regression: inside `pair[A, B]`, a receiver `Validated[string, B]` whose
// method is `Map[B]` has TWO distinct `B`s. Before the fix, substituting the
// receiver arg `B` into the method's declared param `func(A) B` made the method
// result param textually identical to the receiver element, so unification
// bound the method param from the lambda's *parameter* position and left the
// call's result type as `Validated[string, B]` instead of
// `Validated[string, Tuple[A, B]]`. That surfaced as a wrong lambda return
// annotation (`func(a A) Validated[string, B]`) which fails to compile in Go.
func TestNestedGenericMethodResultInference(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	validatedDecl := `package main

import . "martianoff/gala/std"

type Validated[E any, A any] struct {
    Err E
    Val A
    Ok bool
}

func (v Validated[E, A]) FlatMap[B any](f func(A) Validated[E, B]) Validated[E, B] {
    if v.Ok {
        return f(v.Val)
    }
    return Validated[E, B]{Err: v.Err, Ok: false}
}

func (v Validated[E, A]) Map[B any](f func(A) B) Validated[E, B] {
    if v.Ok {
        return Validated[E, B]{Val: f(v.Val), Ok: true}
    }
    return Validated[E, B]{Err: v.Err, Ok: false}
}
`

	tests := []struct {
		name     string
		input    string
		contains []string
		// notContains guards the collapsed type param from reappearing.
		notContains []string
	}{
		{
			// Repro A: FlatMap's result type param must resolve to the inner
			// Map lambda's return type Tuple[A, B], not collapse to the
			// enclosing function's B.
			name: "FlatMap result param from nested Map lambda return",
			input: validatedDecl + `
func pair[A any, B any](va Validated[string, A], vb Validated[string, B]) Validated[string, Tuple[A, B]] =
    va.FlatMap((a) => vb.Map((b) => Tuple(a, b)))
`,
			contains: []string{
				"func(a A) Validated[string, Tuple[A, B]]",
				"func(b B) Tuple[A, B]",
			},
			notContains: []string{
				"func(a A) Validated[string, B]",
			},
		},
		{
			// Repro B: mapping over a nested-generic receiver must keep the
			// receiver's element type Tuple[B, C] intact for the lambda param,
			// never collapsing B into C.
			name: "Map lambda param keeps nested receiver element type",
			input: validatedDecl + `
func flatten[A any, B any, C any](vbc Validated[string, Tuple[B, C]], a A) Validated[string, Tuple3[A, B, C]] =
    vbc.Map((bc) => Tuple(a, bc.V1, bc.V2))
`,
			contains: []string{
				"func(bc Tuple[B, C]) Tuple3[A, B, C]",
			},
			notContains: []string{
				"func(bc Tuple[C, C])",
				"func(bc Tuple[B, B])",
			},
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
				assert.NotContains(t, got, bad, "generated Go must not contain collapsed type %q", bad)
			}
		})
	}
}
