package parser

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestIllegalSourceCharacters covers GALA-E0051: characters Go source may not
// contain are rejected at their position. Inside a literal they used to reach
// the generated Go verbatim — NUL and a stray BOM as an internal transpiler
// error, invalid UTF-8 as a silently changed string value.
func TestIllegalSourceCharacters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		line     int
		col      int
		contains string
	}{
		{
			name:     "NUL in a string literal",
			input:    "package main\n\nval s = \"a\x00b\"\n",
			line:     3,
			col:      10,
			contains: "illegal character NUL",
		},
		{
			name:     "NUL in a raw string",
			input:    "package main\n\nval s = `\x00`\n",
			line:     3,
			col:      9,
			contains: "illegal character NUL",
		},
		{
			name:     "byte order mark inside a string",
			input:    "package main\n\nval s = \"" + utf8BOM + "\"\n",
			line:     3,
			col:      9,
			contains: "illegal byte order mark",
		},
		{
			name:     "second byte order mark after the leading one",
			input:    utf8BOM + utf8BOM + "package main\n",
			line:     1,
			col:      0,
			contains: "illegal byte order mark",
		},
		{
			name:     "invalid UTF-8 in a string",
			input:    "package main\n\nval s = \"a\xffb\"\n",
			line:     3,
			col:      10,
			contains: "invalid UTF-8 encoding (byte 0xFF)",
		},
		{
			name: "column counts code points before the bad byte",
			// "é" and "日" are one column each, like every other diagnostic.
			input:    "package main\n\nval s = \"é日\xc3\"\n",
			line:     3,
			col:      11,
			contains: "invalid UTF-8 encoding (byte 0xC3)",
		},
		{
			name:     "invalid UTF-8 in a comment",
			input:    "package main\n\n// caf\xe9\nval s = 1\n",
			line:     3,
			col:      6,
			contains: "invalid UTF-8 encoding (byte 0xE9)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := NewAntlrGalaParser().Parse(tt.input)
			require.Error(t, err)
			var multi *galaerr.MultiError
			require.True(t, errors.As(err, &multi), "want a MultiError, got %T", err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(multi.Errors[0], &se), "the first diagnostic should be the source-character error, got %v", multi.Errors[0])
			assert.Equal(t, galaerr.CodeIllegalSourceCharacter, se.Code)
			assert.Equal(t, tt.line, se.Line)
			assert.Equal(t, tt.col, se.Column)
			assert.Equal(t, tt.col+1, se.EndColumn)
			assert.Contains(t, se.Msg, tt.contains)
		})
	}
}

// TestLegalSourceCharacters guards the other side: a single leading BOM,
// escapes that spell the illegal values, and ordinary non-ASCII text all parse.
func TestLegalSourceCharacters(t *testing.T) {
	for _, input := range []string{
		utf8BOM + "package main\n\nval s = \"x\"\n",
		"package main\n\nval s = \"\\x00\\uFEFF\\xff\"\n",
		"package main\n\n// é ✓ 日本 𝄞\nval s = \"é ✓ 日本 𝄞\"\n",
		utf8BOM + "package main\r\n\r\n// é\r\nval s = \"é\"\r\n",
	} {
		_, _, err := NewAntlrGalaParser().Parse(input)
		assert.NoError(t, err, "input %q", input)
	}
}

// TestIllegalCharacterBetweenTokensReportedOnce: outside a literal the lexer
// also fails on the character; its token recognition error at the same spot
// is dropped so the character is reported once, as GALA-E0051.
func TestIllegalCharacterBetweenTokensReportedOnce(t *testing.T) {
	for _, input := range []string{
		"package main\n\nval s = 1\x00\n",
		"package main\n\nval s = 1" + utf8BOM + "\n",
	} {
		_, _, errs := NewAntlrGalaParser().ParseLenient(input)
		require.NotEmpty(t, errs, "input %q", input)
		var se *galaerr.SemanticError
		require.ErrorAs(t, errs[0], &se)
		assert.Equal(t, galaerr.CodeIllegalSourceCharacter, se.Code)
		for _, err := range errs[1:] {
			var syn *galaerr.SyntaxError
			if errors.As(err, &syn) {
				assert.False(t, syn.Line == se.Line && syn.Column == se.Column,
					"input %q: the character is reported twice: %v", input, errs)
			}
		}
	}
}
