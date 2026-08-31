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

func newInterpolationDiagnosticsTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestMalformedInterpolationIsRejected pins B1: an embedded expression that does
// not parse must fail the build. parseAndTransformExpr used to strip the ANTLR
// error listeners without installing a replacement and then discard the errors
// on the recovered tree, so `s"v=${x +}"` compiled and silently printed `v=1` —
// the trailing operator was dropped. Invalid source must never reach codegen.
func TestMalformedInterpolationIsRejected(t *testing.T) {
	trans := newInterpolationDiagnosticsTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "trailing_binary_operator",
			input: "package main\n\nfunc main() {\n    val x = 1\n    Println(s\"v=${x +}\")\n}",
		},
		{
			name:  "unbalanced_paren",
			input: "package main\n\nfunc main() {\n    val x = 1\n    Println(s\"v=${(x}\")\n}",
		},
		{
			name:  "format_string_too",
			input: "package main\n\nfunc main() {\n    val x = 1\n    Println(f\"v=${x *}%d\")\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.Error(t, err,
				"a malformed interpolated expression must not transpile")
		})
	}
}

// TestDiagnosticInsideInterpolationCarriesRealPosition pins B2: an error raised
// while transforming an embedded expression must point at the line the user
// wrote it on. The embedded text used to be re-parsed into a fresh InputStream
// with no offset, so every such diagnostic reported line 1 column 0 — a bare
// `len()` on line 6 put its caret on `package main`.
func TestDiagnosticInsideInterpolationCarriesRealPosition(t *testing.T) {
	trans := newInterpolationDiagnosticsTranspiler()

	// `len` is forbidden (GALA-E0035). Here it sits inside an interpolation on
	// line 6, so the diagnostic must say line 6 — not line 1.
	input := "package main\n" + // 1
		"\n" + // 2
		"func main() {\n" + // 3
		"    val s = \"hello\"\n" + // 4
		"    Println(\"start\")\n" + // 5
		"    Println(s\"n=${len(s)}\")\n" + // 6
		"}\n" // 7

	_, err := trans.Transpile(input, "")
	require.Error(t, err, "bare len inside an interpolation must still be rejected")
	require.Contains(t, err.Error(), "GALA-E0035", "expected the forbidden-builtin code")
	require.Contains(t, err.Error(), "line 6",
		"the diagnostic must report the line the interpolation is on, got: %v", err)
	require.False(t, strings.Contains(err.Error(), "line 1:"),
		"the diagnostic must not collapse to line 1, got: %v", err)
}
