package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestRichDiagnostic_ForbiddenBuiltin drives a real bare-`len` program through
// the full transpiler pipeline and feeds the resulting error to the CLI's rich
// renderer, asserting the framed snippet, the GALA-E0035 code, and a caret that
// underlines the `len` token exactly (exact-span upgrade).
func TestRichDiagnostic_ForbiddenBuiltin(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	src := "package main\n\nfunc f(s string) int = len(s)"
	_, err := trans.Transpile(src, "")
	require.Error(t, err, "expected bare len(...) to be rejected")

	out := galaerr.RenderRich(err, galaerr.Options{
		FallbackPath:   "prog.gala",
		FallbackSource: src,
	})

	require.Contains(t, out, "GALA-E0035", "rendered output must carry the code:\n%s", out)
	require.Contains(t, out, "  --> prog.gala:", "rendered output must show the locus:\n%s", out)
	require.Contains(t, out, "^^^", "rendered output must draw a caret:\n%s", out)
	require.Contains(t, out, "len(s)", "rendered output must frame the source line:\n%s", out)

	// The caret must sit exactly under `len`: the caret indent length equals
	// the index of `len` on the framed source line.
	var snippet, caret string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "len(s)") && strings.Contains(ln, "| ") {
			snippet = ln
		}
		if strings.Contains(ln, "^^^") {
			caret = ln
		}
	}
	require.NotEmpty(t, snippet, "no framed source line found:\n%s", out)
	require.NotEmpty(t, caret, "no caret line found:\n%s", out)

	snippetBody := snippet[strings.Index(snippet, "| ")+2:]
	caretBody := caret[strings.Index(caret, "| ")+2:]
	indent := len(caretBody) - len(strings.TrimLeft(caretBody, " "))
	require.GreaterOrEqual(t, len(snippetBody), indent+3)
	require.Equal(t, "len", snippetBody[indent:indent+3],
		"caret is not aligned under `len`:\nsnippet=%q\ncaret=%q", snippetBody, caretBody)
	// Exact span: three carets, no more.
	require.Contains(t, caretBody, "^^^")
	require.NotContains(t, caretBody, "^^^^")
}
