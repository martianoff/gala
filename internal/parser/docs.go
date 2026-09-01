package parser

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"
)

// Doc comments
// ------------
// GALA follows Go's doc-comment convention: a run of `//` lines (or a single
// `/* */` block) immediately above a declaration documents it. No dedicated
// syntax — the stdlib already writes docs this way.
//
// The lexer routes comments to the hidden channel (see the COMMENT and
// MULTILINE_COMMENT rules in gala.g4), so the parser never sees them and the
// grammar is unaffected. extractDocComments walks the filled token stream after
// the parse and pairs each comment run with the declaration it precedes. This
// costs one linear token walk; the tokens are already materialized, so no extra
// lexing happens.
//
// Attachment rules mirror go/parser's:
//   - A comment that begins on the same line as preceding code is a trailing
//     comment, not documentation.
//   - A blank line between the comment run and the declaration severs it.
//   - //line and //go: lines are directives; they contribute no prose but do
//     not sever a run that continues past them.
//   - The run must end on the line immediately above the declaration.
//
// The result is keyed by the character offset of the declaration's first token
// (the `func` / `type` / `sealed` / `val` keyword, or `package`), which is what
// the analyzer has available at every site where it records a SourcePos. Keying
// by offset rather than line keeps the lookup exact even when two declarations
// share a line.

// extractDocComments pairs hidden-channel comment runs with the declarations
// they document. toks must be the complete token stream, hidden tokens included.
func extractDocComments(toks []antlr.Token) map[int]string {
	docs := make(map[int]string)

	var block []string
	blockEndLine := -1  // last source line covered by the pending comment run
	lastCodeLine := -1  // last source line covered by a code token

	for _, tk := range toks {
		if tk.GetTokenType() == antlr.TokenEOF {
			break
		}
		text := tk.GetText()
		startLine := tk.GetLine()
		endLine := startLine + strings.Count(text, "\n")

		if tk.GetChannel() != antlr.TokenDefaultChannel {
			// Hidden channel holds comments only: WS is skipped outright.
			if startLine <= lastCodeLine {
				continue // trailing comment on a line that already had code
			}
			if blockEndLine >= 0 && startLine != blockEndLine+1 {
				block = block[:0] // a blank line severed the run
			}
			block = append(block, docCommentLines(text)...)
			blockEndLine = endLine
			continue
		}

		if len(block) > 0 && startLine == blockEndLine+1 {
			docs[tk.GetStart()] = strings.Join(block, "\n")
		}
		block = block[:0]
		blockEndLine = -1
		lastCodeLine = endLine
	}
	return docs
}

// docCommentLines strips comment markers and returns the prose lines of a single
// comment token. Returns nil for pragma lines, which carry no documentation.
func docCommentLines(raw string) []string {
	raw = strings.TrimSpace(raw)

	if rest, ok := strings.CutPrefix(raw, "//"); ok {
		if isPragmaBody(rest) {
			return nil
		}
		return []string{strings.TrimPrefix(rest, " ")}
	}

	rest, ok := strings.CutPrefix(raw, "/*")
	if !ok {
		return nil
	}
	rest = strings.TrimSuffix(rest, "*/")
	var out []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimRight(line, " \t\r")
		line = strings.TrimLeft(line, " \t")
		// Strip the leading star of the conventional boxed block-comment style.
		if star, ok := strings.CutPrefix(line, "*"); ok {
			line = strings.TrimPrefix(star, " ")
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// isPragmaBody reports whether a `//` comment body is a compiler directive
// rather than prose. Mirrors the forms the transpiler itself emits.
func isPragmaBody(body string) bool {
	return strings.HasPrefix(body, "go:") ||
		strings.HasPrefix(body, "gala:") ||
		body == "line" ||
		strings.HasPrefix(body, "line ")
}
