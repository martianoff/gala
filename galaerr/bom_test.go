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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, galaerr.StripBOM(tt.input))
		})
	}
}

// TestStripBOMIsIdempotentOnStrippedInput guards the "exactly one" contract
// from the other side: re-stripping already-clean text must be a no-op.
func TestStripBOMIsIdempotentOnStrippedInput(t *testing.T) {
	once := galaerr.StripBOM(utf8BOM + "package main\n")
	assert.Equal(t, once, galaerr.StripBOM(once))
}

// TestRenderRichBOMSource covers the diagnostic renderer's own re-read of the
// source: a BOM'd file must produce the same frame — and the same caret
// column — as the identical file without a BOM. Without stripping, the caret
// on line 1 sits one rune off for every diagnostic in the file, not just
// BOM-related ones.
func TestRenderRichBOMSource(t *testing.T) {
	const src = "val n = len(s)\n"

	// `len` starts at 0-based rune column 8 on line 1; exact span 8..11.
	newErr := func() error {
		return galaerr.NewCodedSemanticError(
			galaerr.CodeForbiddenGoBuiltin, 1, 8,
			"bare `len` is forbidden",
			"GALA strings & collections expose .Size()",
		).WithSpan(11)
	}

	plain := galaerr.RenderRich(newErr(), galaerr.Options{FallbackSource: src})
	bommed := galaerr.RenderRich(newErr(), galaerr.Options{FallbackSource: utf8BOM + src})

	assert.Equal(t, plain, bommed, "a leading BOM must not change the rendered diagnostic")

	// And assert the alignment absolutely, so the test still means something if
	// both paths were to regress together.
	assert.Contains(t, bommed, "1 | val n = len(s)")
	assert.NotContains(t, bommed, utf8BOM, "the BOM must not leak into the rendered frame")

	caret := caretLineOf(bommed)
	require.NotEmpty(t, caret, "expected a caret line")
	assert.Equal(t,
		strings.Repeat(" ", 8)+"^^^ GALA strings & collections expose .Size()",
		afterPipe(caret))
}

// TestRenderRichBOMSourceFromFile covers the on-disk branch of resolveSource,
// which re-reads the file independently of whatever text the parser was given.
func TestRenderRichBOMSourceFromFile(t *testing.T) {
	const src = "val n = len(s)\n"

	write := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "src.gala")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}

	newErr := func(path string) error {
		return galaerr.WithFilePath(galaerr.NewCodedSemanticError(
			galaerr.CodeForbiddenGoBuiltin, 1, 8, "bare `len` is forbidden", "use .Size()",
		).WithSpan(11), path)
	}

	plainPath := write(t, src)
	bomPath := write(t, utf8BOM+src)

	plain := galaerr.RenderRich(newErr(plainPath), galaerr.Options{})
	bommed := galaerr.RenderRich(newErr(bomPath), galaerr.Options{})

	// The locus line differs (different temp paths), so compare the frame body.
	assert.Contains(t, bommed, "1 | val n = len(s)")
	assert.NotContains(t, bommed, utf8BOM)
	assert.Equal(t, afterPipe(caretLineOf(plain)), afterPipe(caretLineOf(bommed)))
}
