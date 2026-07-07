package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBracketBareLambdaInference is the repro for the second inference gap the
// migration papered over (with `(r int) => …` annotations): a call to the
// generic `resource.Bracket` with BARE, unannotated lambdas must infer their
// parameter type from the concrete resource argument. Because Bracket now takes
// the resource eagerly (`Bracket[R,A](resource R, release func(R), body func(R) A)`),
// R binds from a concrete non-lambda argument, so the release/body lambdas get
// a concrete parameter type instead of leaking the type parameter `R` or
// defaulting to `any`.
func TestBracketBareLambdaInference(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	input := "package main\n\n" +
		"import . \"martianoff/gala/resource\"\n\n" +
		"func f() int = Bracket(7, (r) => {}, (r) => r * 3)"

	out, err := trans.Transpile(input, "")
	require.NoError(t, err, "bare-lambda Bracket must transpile")
	require.Contains(t, out, "func(r int)",
		"the body lambda parameter should infer int from the concrete resource arg:\n%s", out)
	require.False(t, strings.Contains(out, "func(r R)"),
		"the type parameter R must not leak into the generated lambda:\n%s", out)
	require.False(t, strings.Contains(out, "func(r any)"),
		"the lambda parameter must not fall back to any:\n%s", out)
}
