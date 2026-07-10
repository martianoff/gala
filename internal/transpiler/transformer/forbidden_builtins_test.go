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

// TestForbiddenGoBuiltinsAreHardErrors verifies that bare Go builtins are
// rejected with GALA-E0035. It covers the builtins whose call sites parse as
// ordinary value-argument calls; make/new (type args) share the identical
// checkForbiddenGoBuiltinCall path.
func TestForbiddenGoBuiltinsAreHardErrors(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	rejected := []struct {
		name  string
		input string
	}{
		{"len", "package main\n\nfunc f(s string) int = len(s)"},
		{"append", "package main\n\nfunc f(xs []int, x int) []int = append(xs, x)"},
		{"cap", "package main\n\nfunc f(xs []int) int = cap(xs)"},
		{"copy", "package main\n\nfunc f(dst []int, src []int) int = copy(dst, src)"},
		{"delete", "package main\n\nfunc f(m map[string]int) {\n    delete(m, \"k\")\n}"},
		{"complex", "package main\n\nfunc f(re float64, im float64) = complex(re, im)"},
		{"real", "package main\n\nfunc f(c complex128) float64 = real(c)"},
		{"imag", "package main\n\nfunc f(c complex128) float64 = imag(c)"},
		{"panic", "package main\n\nfunc f() int {\n    panic(\"boom\")\n}"},
		{"recover", "package main\n\nfunc f() {\n    recover()\n}"},
	}
	for _, tc := range rejected {
		tc := tc
		t.Run("rejects_bare_"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.Error(t, err, "expected bare %q to be rejected", tc.name)
			require.Contains(t, err.Error(), "GALA-E0035",
				"expected the forbidden-builtin error code for %q, got: %v", tc.name, err)
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
		fn    string
		input string
	}{
		{"delete", "package main\n\nfunc delete(x int) int = x + 1\n\nfunc main() {\n    Println(delete(41))\n}"},
		{"copy", "package main\n\nfunc copy(x int) int = x * 2\n\nfunc main() {\n    Println(copy(21))\n}"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("user_"+tc.fn, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err, "user-defined %q must be legal", tc.fn)
			require.Contains(t, out, tc.fn+"(",
				"call to user function %q should be preserved", tc.fn)
		})
	}
}

// TestSizeSugarStillTranspiles confirms the sanctioned replacements are
// accepted where the bare builtins are not: `.Size()` lowers to a length call,
// `.ByteSize()` to len, and the Panic wrapper to Go's builtin panic.
func TestSizeSugarStillTranspiles(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	t.Run("string_Size_lowers_to_RuneCountInString", func(t *testing.T) {
		out, err := trans.Transpile("package main\n\nfunc f(s string) int = s.Size()", "")
		require.NoError(t, err)
		require.Contains(t, out, "RuneCountInString")
	})
	t.Run("string_ByteSize_lowers_to_len", func(t *testing.T) {
		out, err := trans.Transpile("package main\n\nfunc f(s string) int = s.ByteSize()", "")
		require.NoError(t, err)
		require.Contains(t, out, "len(")
	})
	t.Run("Panic_wrapper_lowers_to_builtin_panic", func(t *testing.T) {
		out, err := trans.Transpile("package main\n\nfunc f() int {\n    Panic(\"boom\")\n}", "")
		require.NoError(t, err)
		require.True(t, strings.Contains(out, "panic("),
			"Panic wrapper should lower to Go builtin panic:\n%s", out)
	})
}
