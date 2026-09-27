package parser

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"martianoff/gala/galaerr"
)

// checkSourceChars enforces Go's source-text rules on GALA source: the text
// must be valid UTF-8 and must not contain a NUL character or a byte order
// mark. (A single leading BOM is allowed, as in Go; callers strip it first.)
//
// GALA copies a literal's raw text verbatim into the generated Go, so any of
// these inside a string, rune or raw-string literal used to reach Go source,
// where they are illegal. NUL and U+FEFF surfaced as an internal transpiler
// error (the generated file did not parse). Invalid UTF-8 was worse: the lexer
// decodes its input into runes, so every invalid byte silently became U+FFFD
// and the program compiled with a different string value than the one written.
//
// input must already have its leading BOM removed. The returned diagnostic is
// positioned the way ANTLR positions tokens — 1-based line, 0-based column
// counted in code points, with each invalid byte counting as one — so it lines
// up with every other diagnostic on the same line.
func checkSourceChars(input string) *galaerr.SemanticError {
	// Every parse runs this, the LSP's on each keystroke: settle the common,
	// clean case with the library's vectorised scans, and walk the text rune
	// by rune only to position a violation.
	if utf8.ValidString(input) && strings.IndexByte(input, 0) < 0 && !strings.Contains(input, "\uFEFF") {
		return nil
	}
	line, col := 1, 0
	for i := 0; i < len(input); {
		r, size := utf8.DecodeRuneInString(input[i:])
		var msg string
		switch {
		case r == utf8.RuneError && size == 1:
			msg = fmt.Sprintf("invalid UTF-8 encoding (byte 0x%02X)", input[i])
		case r == 0:
			msg = "illegal character NUL"
		case r == 0xFEFF:
			msg = "illegal byte order mark (U+FEFF is only allowed as the first character of a file)"
		}
		if msg != "" {
			return galaerr.NewCodedSemanticError(
				galaerr.CodeIllegalSourceCharacter,
				line,
				col,
				msg,
				"GALA source must be valid UTF-8 without NUL characters or byte order marks, as Go source must; "+
					"to put such a value in a string, write it as an escape (\\x00, \\uFEFF, \\xFF)",
			).WithSpan(col + 1)
		}
		if r == '\n' {
			line, col = line+1, 0
		} else {
			col++
		}
		i += size
	}
	return nil
}
