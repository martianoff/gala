package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSizeSugarOnGoFunctionReturn is the repro for the inference gap the
// go-builtins migration papered over with `val runes []rune = …` annotations:
// the `.Size()` / `.ByteSize()` sugar must fire on the result of an ordinary
// dot-imported Go function (here go_interop.ToRunes, which returns []rune) with
// NO type annotation, in both the val-bound and direct-call forms. Before the
// fix, inferCallIdentType returned NilType for a bare Go function name, so
// sizeSugarReceiverType could not classify the receiver and the sugar left a
// raw `.Size()` method call on `[]rune` (which has no such method).
func TestSizeSugarOnGoFunctionReturn(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "val-bound Go slice return",
			input: "package main\n\n" +
				"import . \"martianoff/gala/go_interop\"\n\n" +
				"func f() int {\n    val r = ToRunes(\"hi\")\n    return r.Size()\n}",
		},
		{
			name: "direct Go slice return",
			input: "package main\n\n" +
				"import . \"martianoff/gala/go_interop\"\n\n" +
				"func g() int = ToRunes(\"hi\").Size()",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			// The sugar must have fired: a []rune receiver lowers .Size() to len().
			require.Contains(t, out, "len(",
				".Size() on a Go []rune return should lower to len():\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() method call should survive on the Go slice:\n%s", out)
		})
	}
}

// TestSizeSugarOnMultiReturnBinding covers a destructuring binding
// `val x, err = goCall()`, where each name must take its corresponding return
// type. Before the fix the value component was left NilType, so `.Size()` on it
// could not fire. Uses os.ReadFile, which returns ([]byte, error).
func TestSizeSugarOnMultiReturnBinding(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	input := "package main\n\n" +
		"import \"os\"\n\n" +
		"func f() int {\n    val data, err = os.ReadFile(\"x\")\n    if err != nil {}\n    return data.Size()\n}"

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	require.Contains(t, out, "len(",
		".Size() on the []byte value of a (T, error) return should lower to len():\n%s", out)
	require.False(t, strings.Contains(out, ".Size()"),
		"no raw .Size() should survive on the Go []byte binding:\n%s", out)
}

// TestSizeResultTypeFlowsThroughCompositeExpr guards the interaction between the
// `.Size()` sugar and the emitted `len(...)`: the sugar lowers to `len(...)`,
// and downstream inference must type that node as int so a surrounding
// if-expression resolves to int rather than being poisoned to `any`. Regression
// for the removed-then-restored `len` inference rule.
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
