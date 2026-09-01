package parser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// utf8BOM is the UTF-8 encoding of U+FEFF. Written as explicit bytes because a
// literal BOM inside a Go source file is itself rejected by the Go parser
// ("illegal byte order mark") when it appears anywhere but the very first
// position.
const utf8BOM = "\xef\xbb\xbf"

// TestParseWithLeadingBOM asserts that a source file prefixed with a UTF-8 byte
// order mark parses exactly like the same file without one. Windows editors and
// PowerShell's UTF-8 encoders emit a BOM by default, and Go — the language GALA
// targets — ignores a leading BOM, so GALA rejecting such a file is a
// gratuitous divergence.
func TestParseWithLeadingBOM(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "package clause and func",
			input: `package main

func main() {
    Println("hi")
}
`,
			wantErr: false,
		},
		{
			name: "package clause and val",
			input: `package main

val x = 10`,
			wantErr: false,
		},
		{
			name: "package, import and declaration",
			input: `package main

import "fmt"

val x = 10`,
			wantErr: false,
		},
		{
			name: "genuinely missing empty line still errors with a BOM",
			input: `package main
val x = 10`,
			wantErr: true,
		},
		{
			// A BOM'd file is almost always Windows-authored, so CRLF is the
			// realistic pairing rather than an exotic one.
			name:    "CRLF line endings",
			input:   "package main\r\n\r\nval x = 10\r\n",
			wantErr: false,
		},
	}

	p := NewAntlrGalaParser()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, plainErrs := p.ParseLenient(tt.input)
			_, _, bomErrs := p.ParseLenient(utf8BOM + tt.input)

			if tt.wantErr {
				assert.NotEmpty(t, plainErrs, "expected the BOM-free input to fail")
			} else {
				assert.Empty(t, plainErrs, "expected the BOM-free input to parse: %v", plainErrs)
			}

			// The BOM must be invisible: identical diagnostics either way.
			assert.Equal(t, errStrings(plainErrs), errStrings(bomErrs),
				"a leading BOM must not change the diagnostics")

			for _, err := range bomErrs {
				assert.NotContains(t, err.Error(), "token recognition error",
					"the BOM must not reach the lexer")
			}
		})
	}
}

// TestParseWithLeadingBOMNoEmptyLineCascade pins the specific misleading
// cascade a BOM used to produce: on top of the lexer error, checkEmptyLines
// reported a missing empty line after the package clause on a file that plainly
// has one — pointing the reader at correct code. (The multi-byte offset
// handling that made the BOM shift that check is covered on its own by
// TestEmptyLineRequirement.)
func TestParseWithLeadingBOMNoEmptyLineCascade(t *testing.T) {
	const src = `package main

func main() {
    Println("hi")
}
`

	tree, _, errs := NewAntlrGalaParser().ParseLenient(utf8BOM + src)

	require.NotNil(t, tree)
	for _, err := range errs {
		assert.NotContains(t, err.Error(), "packageClause should follow by an empty line",
			"the empty line after `package main` is present; this diagnostic is false")
	}
	assert.Empty(t, errs, "a BOM'd file that is otherwise valid must parse cleanly")
}

// TestParseWithLeadingBOMPreservesPositions checks that stripping the BOM does
// not shift reported positions: a diagnostic on line 1 must keep its column so
// the rendered caret stays aligned.
func TestParseWithLeadingBOMPreservesPositions(t *testing.T) {
	// A bad token on line 1 (before the package clause) gives us a line-1 error
	// whose column we can compare across both inputs.
	const src = "@@@\n\npackage main\n"

	_, _, plainErrs := NewAntlrGalaParser().ParseLenient(src)
	_, _, bomErrs := NewAntlrGalaParser().ParseLenient(utf8BOM + src)

	require.NotEmpty(t, plainErrs)
	require.Equal(t, len(plainErrs), len(bomErrs))
	for i := range plainErrs {
		assert.Equal(t, plainErrs[i].Error(), bomErrs[i].Error(),
			"BOM must not shift reported line/column")
	}
}

// TestParseBOMOnlyAtStartIsStripped confirms only a leading BOM is special: a
// U+FEFF inside a string literal is ordinary content and must survive parsing.
func TestParseBOMOnlyAtStartIsStripped(t *testing.T) {
	src := "package main\n\nval s = \"a" + utf8BOM + "b\"\n"

	_, _, errs := NewAntlrGalaParser().ParseLenient(src)
	assert.Empty(t, errs, "a BOM inside a string literal is content, not a marker: %v", errs)
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, strings.TrimSpace(err.Error()))
	}
	return out
}
