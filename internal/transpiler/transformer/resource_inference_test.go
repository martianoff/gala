package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEagerResourceBareLambdaInference is the hermetic repro for the second
// inference gap the migration papered over (with `(r int) => …` annotations):
// a call to a generic combinator with the eager-resource shape
// `f[R,A](resource R, release func(R), body func(R) A)` must let its BARE,
// unannotated lambdas infer their parameter type from the concrete resource
// argument — R binds from a non-lambda argument, so the release/body lambdas
// get a concrete parameter type instead of leaking the type parameter `R` or
// defaulting to `any`.
//
// The combinator is declared locally in the snippet (no `resource`/`go_interop`
// import and no Go SDK), so the test is hermetic in the transformer unit
// sandbox. The resource-package specialization is exercised end to end by
// examples/resource_combinators.gala and resource/resource_test.gala.
func TestEagerResourceBareLambdaInference(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	input := "package main\n\n" +
		"func bracket[R any, A any](res R, release func(R), body func(R) A) A = body(res)\n\n" +
		"func f() int = bracket(7, (r) => {}, (r) => r * 3)"

	out, err := trans.Transpile(input, "")
	require.NoError(t, err, "bare-lambda eager-resource call must transpile")
	require.Contains(t, out, "func(r int)",
		"the body lambda parameter should infer int from the concrete resource arg:\n%s", out)
	require.False(t, strings.Contains(out, "func(r R)"),
		"the type parameter R must not leak into the generated lambda:\n%s", out)
	require.False(t, strings.Contains(out, "func(r any)"),
		"the lambda parameter must not fall back to any:\n%s", out)
}
