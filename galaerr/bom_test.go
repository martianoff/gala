package galaerr_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// utf8BOM is the UTF-8 encoding of U+FEFF. Written as explicit bytes because a
// literal BOM inside a Go source file is itself rejected by the Go parser
// ("illegal byte order mark") when it appears anywhere but the very first
// position.
const utf8BOM = "\xef\xbb\xbf"

func TestStripBOM(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "BOM-free input is untouched",
			input: "package main\n",
			want:  "package main\n",
		},
		{
			name:  "single leading BOM is stripped",
			input: utf8BOM + "package main\n",
			want:  "package main\n",
		},
		{
			name:  "only the BOM",
			input: utf8BOM,
			want:  "",
		},
		{
			name:  "exactly one BOM is stripped, a second is kept",
			input: utf8BOM + utf8BOM + "package main\n",
			want:  utf8BOM + "package main\n",
		},
		{
			name:  "BOM on line 2 is not stripped",
			input: "package main\n" + utf8BOM + "val x = 1\n",
			want:  "package main\n" + utf8BOM + "val x = 1\n",
		},
		{
			name:  "BOM mid-line is not stripped",
			input: "val s = \"a" + utf8BOM + "b\"\n",
			want:  "val s = \"a" + utf8BOM + "b\"\n",
		},
		{
			name:  "leading whitespace before a BOM means it is not a marker",
			input: " " + utf8BOM + "package main\n",
			want:  " " + utf8BOM + "package main\n",
		},
		{
			name:  "a truncated BOM prefix is left alone",
			input: "\xef\xbb" + "package main\n",
			want:  "\xef\xbb" + "package main\n",
		},
		// The two rows below record a deliberate limit: only the UTF-8 marker is
		// removed. A UTF-16 file is UTF-16 throughout, and a double-encoded BOM
		// means the file was already mangled — dropping either prefix would hide
		// a wrong-encoding problem behind a stranger error further in.
		{
			name:  "a UTF-16 BOM is not a UTF-8 BOM",
			input: "\xff\xfep\x00a\x00",
			want:  "\xff\xfep\x00a\x00",
		},
		{
			name:  "a double-encoded BOM is left alone",
			input: "\xc3\xaf\xc2\xbb\xc2\xbfpackage main\n",
			want:  "\xc3\xaf\xc2\xbb\xc2\xbfpackage main\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, galaerr.StripBOM(tt.input))
		})
	}
}

// bomFrameSrc is a one-line source whose `len` sits at 0-based rune column 8,
// so the caret span 8..11 is exact and any offset drift is visible.
const (
	bomFrameSrc  = "val n = len(s)\n"
	bomFrameHint = "GALA strings & collections expose .Size()"
)

// bomLenErr builds the same diagnostic for both resolveSource branches; pass an
// empty path for the FallbackSource branch.
func bomLenErr(path string) error {
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeForbiddenGoBuiltin, 1, 8, "bare `len` is forbidden", bomFrameHint,
	).WithSpan(11)
	if path == "" {
		return err
	}
	return galaerr.WithFilePath(err, path)
}

// assertBOMFrameAligned pins the alignment absolutely, so the tests below still
// mean something if both resolveSource branches were to regress together.
func assertBOMFrameAligned(t *testing.T, rendered string) {
	t.Helper()
	assert.Contains(t, rendered, "1 | val n = len(s)")
	assert.NotContains(t, rendered, utf8BOM, "the BOM must not leak into the rendered frame")

	caret := caretLineOf(rendered)
	require.NotEmpty(t, caret, "expected a caret line")
	assert.Equal(t, strings.Repeat(" ", 8)+"^^^ "+bomFrameHint, afterPipe(caret))
}

// TestRenderRichBOMSource covers the diagnostic renderer's own re-read of the
// source: a BOM'd file must produce the same frame — and the same caret
// column — as the identical file without a BOM. Without stripping, the caret
// on line 1 sits one rune off for every diagnostic in the file, not just
// BOM-related ones.
func TestRenderRichBOMSource(t *testing.T) {
	plain := galaerr.RenderRich(bomLenErr(""), galaerr.Options{FallbackSource: bomFrameSrc})
	bommed := galaerr.RenderRich(bomLenErr(""), galaerr.Options{FallbackSource: utf8BOM + bomFrameSrc})

	assert.Equal(t, plain, bommed, "a leading BOM must not change the rendered diagnostic")
	assertBOMFrameAligned(t, bommed)
}

// TestRenderRichBOMSourceFromFile covers the on-disk branch of resolveSource,
// which re-reads the file independently of whatever text the parser was given.
func TestRenderRichBOMSourceFromFile(t *testing.T) {
	write := func(content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "src.gala")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}

	plain := galaerr.RenderRich(bomLenErr(write(bomFrameSrc)), galaerr.Options{})
	bommed := galaerr.RenderRich(bomLenErr(write(utf8BOM+bomFrameSrc)), galaerr.Options{})

	// Comparing the caret lines alone would prove nothing — the caret is built
	// from the error's column, not from the source — so assert the quoted
	// source row too. That is the part a leaked BOM actually corrupts.
	assertBOMFrameAligned(t, plain)
	assertBOMFrameAligned(t, bommed)
}
