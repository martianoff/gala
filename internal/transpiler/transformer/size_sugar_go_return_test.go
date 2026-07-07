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
