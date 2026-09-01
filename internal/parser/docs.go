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
// Returns nil for a source with no doc comments.
func extractDocComments(toks []antlr.Token) map[int]string {
	var docs map[int]string

	var block []string
	blockEndLine := -1 // last source line covered by the pending comment run

	// The previous default-channel token, and the line it ends on. The end line
	// is computed lazily — only when a comment actually follows — because
	// Token.GetText reallocates the lexeme on every call (the ANTLR runtime
	// caches nothing), and all but a handful of tokens in a file are never
	// followed by a comment. Resolving it eagerly for every token cost one
	// string allocation per lexeme: ~9k per 1,100-line file, against ~160
	// comments actually needing it.
	var prevCode antlr.Token
	prevCodeEndLine := -1

	for _, tk := range toks {
		if tk.GetTokenType() == antlr.TokenEOF {
			break
		}
		startLine := tk.GetLine()

		if tk.GetChannel() != antlr.TokenDefaultChannel {
			// Hidden channel holds comments only: WS is skipped outright.
			if prevCode != nil && prevCodeEndLine < 0 {
				prevCodeEndLine = prevCode.GetLine() + strings.Count(prevCode.GetText(), "\n")
			}
			if startLine == prevCodeEndLine {
				continue // trailing comment on a line that already had code
			}
			text := tk.GetText()
			if startLine != blockEndLine+1 {
				block = block[:0] // a blank line severed the run
			}
			block = appendDocLines(block, text)
			blockEndLine = startLine + strings.Count(text, "\n")
			continue
		}

		if len(block) > 0 && startLine == blockEndLine+1 {
			if docs == nil {
				docs = make(map[int]string)
			}
			docs[tk.GetStart()] = strings.Join(block, "\n")
		}
		block = block[:0]
		blockEndLine = -1
		prevCode = tk
		prevCodeEndLine = -1
	}
	return docs
}

// appendDocLines appends the prose lines of a single comment token to dst,
// stripping comment markers. Pragma lines contribute nothing but still count as
// part of the run, so a directive between prose and its declaration does not
// sever the doc comment.
func appendDocLines(dst []string, raw string) []string {
	raw = strings.TrimSpace(raw)

	if rest, ok := strings.CutPrefix(raw, "//"); ok {
		if isPragmaBody(rest) {
			return dst
		}
		return append(dst, strings.TrimPrefix(rest, " "))
	}

	rest, ok := strings.CutPrefix(raw, "/*")
	if !ok {
		return dst
	}
	start := len(dst)
	for _, line := range strings.Split(strings.TrimSuffix(rest, "*/"), "\n") {
		// Strip the leading star of the conventional boxed block-comment style.
		line = strings.TrimSpace(line)
		if star, ok := strings.CutPrefix(line, "*"); ok {
			line = strings.TrimSpace(star)
		}
		dst = append(dst, line)
	}
	// Drop the blank first/last lines a boxed block comment leaves behind.
	for len(dst) > start && dst[start] == "" {
		dst = append(dst[:start], dst[start+1:]...)
	}
	for len(dst) > start && dst[len(dst)-1] == "" {
		dst = dst[:len(dst)-1]
	}
	return dst
}

// isPragmaBody reports whether a `//` comment body is a compiler directive
// rather than prose. Covers the forms the transpiler itself emits (see
// insertLineDirectives and insertEmbedDirectives) plus Go's own `//go:` family,
// which GALA source may carry through to the generated output.
func isPragmaBody(body string) bool {
	return strings.HasPrefix(body, "go:") ||
		body == "line" ||
		strings.HasPrefix(body, "line ")
}
