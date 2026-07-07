package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

func newForbiddenBuiltinTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestForbiddenGoBuiltinsAreHardErrors verifies that every bare Go builtin is
// rejected with GALA-E0035, and that the resolver-aware guard leaves
// user-defined functions of the same name (delete/copy) alone.
func TestForbiddenGoBuiltinsAreHardErrors(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	rejected := []struct {
		name  string
		input string
	}{
		{"len", `package main
func f(s string) int = len(s)`},
		{"append", `package main
func f(xs []int, x int) []int = append(xs, x)`},
		{"make", `package main
func f() []int = make([]int, 0)`},
		{"cap", `package main
func f(xs []int) int = cap(xs)`},
		{"copy", `package main
func f(dst []int, src []int) int = copy(dst, src)`},
		{"delete", `package main
func f(m map[string]int) = delete(m, "k")`},
		{"close", `package main
func f(ch chan int) = close(ch)`},
		{"complex", `package main
func f() complex128 = complex(1.0, 2.0)`},
		{"real", `package main
func f(c complex128) float64 = real(c)`},
		{"imag", `package main
func f(c complex128) float64 = imag(c)`},
		{"panic", `package main
func f() int {
    panic("boom")
}`},
	}
	for _, tc := range rejected {
		tc := tc
		t.Run("rejects_bare_"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.Error(t, err, "expected bare %q to be rejected", tc.name)
			require.Contains(t, err.Error(), "GALA-E0035",
				"expected the forbidden-builtin error code for %q", tc.name)
		})
	}
}

// TestUserFunctionsShadowingBuiltinsAreLegal verifies the resolver-aware guard:
// a user-defined function whose name collides with a Go builtin (delete, copy)
// is not the builtin and must transpile cleanly. Mirrors examples/kvstore.gala
// (`func delete`) and examples/method_default_params.gala (`func copy`).
func TestUserFunctionsShadowingBuiltinsAreLegal(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{"user delete", `package main
func delete(x int) int = x + 1
func main() {
    Println(delete(41))
}`},
		{"user copy", `package main
func copy(x int) int = x * 2
func main() {
    Println(copy(21))
}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err, "user-defined %q must be legal", tc.name)
			require.Contains(t, out, tc.name[len("user "):]+"(",
				"call to user function should be preserved")
		})
	}
}

// TestSizeSugarStillTranspiles confirms the sanctioned replacements are
// accepted where the bare builtins are not: `.Size()` lowers to a length call,
// `.ByteSize()` to len, and the Panic wrapper to Go's builtin panic.
func TestSizeSugarStillTranspiles(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	t.Run("string .Size() lowers to utf8.RuneCountInString", func(t *testing.T) {
		out, err := trans.Transpile(`package main
func f(s string) int = s.Size()`, "")
		require.NoError(t, err)
		require.Contains(t, out, "RuneCountInString")
	})
	t.Run("string .ByteSize() lowers to len", func(t *testing.T) {
		out, err := trans.Transpile(`package main
func f(s string) int = s.ByteSize()`, "")
		require.NoError(t, err)
		require.Contains(t, out, "len(")
	})
	t.Run("Panic wrapper lowers to builtin panic", func(t *testing.T) {
		out, err := trans.Transpile(`package main
func f() int {
    Panic("boom")
}`, "")
		require.NoError(t, err)
		require.True(t, strings.Contains(out, "panic("),
			"Panic wrapper should lower to Go builtin panic:\n%s", out)
	})
}
