package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSizeSugarLowersSliceReceiverToLen exercises the `.Size()` sugar on a
// receiver whose type the transformer resolves locally — an explicitly-typed
// Go-slice parameter — with NO external package or Go-SDK dependency, so it is
// hermetic in the transformer unit sandbox. It asserts the ArrayType
// classification + `len(...)` lowering that the sugar performs.
//
// The go_interop-specific path this originally targeted — the sugar firing on
// the []rune return of a bare dot-imported `ToRunes(...)` (fixed in
// inferCallIdentType) — needs external-package resolution, so it is covered end
// to end by examples/byte_conversion.gala instead of here.
func TestSizeSugarLowersSliceReceiverToLen(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "[]rune param, direct .Size()",
			input: "package main\n\nfunc f(xs []rune) int = xs.Size()",
		},
		{
			name:  "[]int param, val-bound .Size()",
			input: "package main\n\nfunc f(xs []int) int {\n    val n = xs.Size()\n    return n\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			// The sugar must have fired: a Go-slice receiver lowers .Size() to len().
			require.Contains(t, out, "len(",
				".Size() on a Go slice receiver should lower to len():\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() method call should survive on the Go slice:\n%s", out)
		})
	}
}

// TestSizeResultTypeFlowsThroughCompositeExpr guards the interaction between the
// `.Size()` sugar and the emitted `len(...)` / `utf8.RuneCountInString(...)`:
// the sugar lowers to those calls, and downstream inference must type them as
// int so a surrounding if-expression resolves to int rather than being poisoned
// to `any`. Regression for the removed-then-restored `len` inference rule (and
// the added `utf8.RuneCountInString` rule). Hermetic: the receiver is a plain
// `string` parameter, no external package or Go SDK required.
func TestSizeResultTypeFlowsThroughCompositeExpr(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	input := "package main\n\n" +
		"func f(s string) int {\n" +
		"    val n = if (s.Size() > 0) s.Size() - 1 else s.Size()\n" +
		"    return n\n" +
		"}"

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	// The if-expression IIFE must return int, not any.
	require.Contains(t, out, "func() int",
		"the if-expression over .Size() results should infer int, not any:\n%s", out)
	require.False(t, strings.Contains(out, "func() any"),
		"the if-expression result type must not collapse to any:\n%s", out)
}
