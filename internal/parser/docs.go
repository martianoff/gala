package parser

import (
	"go/build/constraint"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/native"
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
//   - //line, //go: and // +build lines are directives; they contribute no prose but do
//     not sever a run that continues past them.
//   - The run must end on the line immediately above the declaration.
//
// The result is keyed by where the declaration's first token starts (the
// `func` / `type` / `sealed` / `val` keyword, or `package`): its line and column
// (see DocKey), which the analyzer has at every site where it records a
// SourcePos. The column keeps the lookup exact even when two declarations share
// a line.

// DocKey is where a declaration's first token starts: its line (from 1) and
// column (from 0). Both trees report a token's position this way, while their
// offsets differ (code points in ANTLR's, bytes in the native tree), so doc
// comments are keyed by it.
type DocKey struct {
	Line, Column int
}

// Docs is a file's doc comments, by the start of the declaration each one
// documents.
type Docs = map[DocKey]string

// DocKeyOf is the key of a declaration whose first token is t.
func DocKeyOf(t antlr.Token) DocKey { return DocKey{t.GetLine(), t.GetColumn()} }

// extractDocComments pairs hidden-channel comment runs with the declarations
// they document. toks must be the complete token stream, hidden tokens included.
// Returns nil for a source with no doc comments.
func extractDocComments(toks []antlr.Token) Docs {
	run := newDocRun()

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
		if tk.GetChannel() != antlr.TokenDefaultChannel {
			// Hidden channel holds comments only: WS is skipped outright.
			if prevCode != nil && prevCodeEndLine < 0 {
				prevCodeEndLine = prevCode.GetLine() + strings.Count(prevCode.GetText(), "\n")
			}
			run.comment(tk.GetLine(), prevCodeEndLine, tk.GetText)
			continue
		}
		run.code(DocKeyOf(tk))
		prevCode = tk
		prevCodeEndLine = -1
	}
	return run.docs
}

// nativeDocComments is extractDocComments for galop's native tokens: the
// same doc comments, by the same keys.
func nativeDocComments(ts *native.GalaTokens) Docs {
	run := newDocRun()
	hidden := ts.Hidden
	for i, tk := range ts.Tokens {
		if tk.Type == native.GalaEOF {
			break
		}
		for ; len(hidden) > 0 && int(hidden[0].Next) == i; hidden = hidden[1:] {
			prevCodeEndLine := -1
			if i > 0 {
				prev := ts.Tokens[i-1]
				prevCodeEndLine = int(prev.Line) + strings.Count(prev.Text(ts.Src), "\n")
			}
			comment := hidden[0].GalaToken
			run.comment(int(comment.Line), prevCodeEndLine, func() string { return comment.Text(ts.Src) })
		}
		run.code(DocKey{Line: int(tk.Line), Column: int(tk.Column)})
	}
	return run.docs
}

// docRun pairs comment runs with the declarations they document, fed a
// token stream's comments (comment) and default-channel tokens (code) in
// order.
type docRun struct {
	docs  Docs
	block []string
	// blockEndLine is the last source line the pending comment run covers.
	blockEndLine int
}

func newDocRun() docRun { return docRun{blockEndLine: -1} }

// comment adds the comment that starts on startLine to the pending run, unless
// it trails code: prevCodeEndLine is the line the code before it ends on, -1
// when there is none. text is called only for a comment the run takes.
func (r *docRun) comment(startLine, prevCodeEndLine int, text func() string) {
	if startLine == prevCodeEndLine {
		return // trailing comment on a line that already had code
	}
	raw := text()
	// Continue the run when this comment sits on the line right after the
	// previous one, or on the same line as where it ended —
	// `/* Deprecated. */ // Use Bar instead.` is one doc comment, not two,
	// and treating it as two silently drops the first.
	if startLine != r.blockEndLine && startLine != r.blockEndLine+1 {
		r.block = r.block[:0] // a blank line severed the run
	}
	r.block = appendDocLines(r.block, raw)
	r.blockEndLine = startLine + strings.Count(raw, "\n")
}

// code ends the pending run at a default-channel token that starts at key:
// the run documents it when it ends on the line just above.
func (r *docRun) code(key DocKey) {
	if len(r.block) > 0 && key.Line == r.blockEndLine+1 {
		if r.docs == nil {
			r.docs = make(Docs)
		}
		r.docs[key] = strings.Join(r.block, "\n")
	}
	r.block = r.block[:0]
	r.blockEndLine = -1
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
		line = strings.TrimRight(line, " \t\r")
		// Strip the leading star of the conventional boxed block-comment style.
		if star, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "*"); ok {
			line = star
		}
		// A `+build` line is a directive here too (see isPragmaBody).
		if constraint.IsPlusBuild("//" + line) {
			continue
		}
		dst = append(dst, line)
	}
	// Remove the indentation the whole block shares, rather than trimming each
	// line to the left margin: an indented run inside a doc comment is a code
	// sample, and flattening it destroys the only thing marking it as one. The
	// `//` path preserves relative indentation for the same reason.
	dedentBlock(dst[start:])
	// Drop the blank first/last lines a boxed block comment leaves behind.
	for len(dst) > start && dst[start] == "" {
		dst = append(dst[:start], dst[start+1:]...)
	}
	for len(dst) > start && dst[len(dst)-1] == "" {
		dst = dst[:len(dst)-1]
	}
	return dst
}

// dedentBlock strips the longest leading run of spaces/tabs common to every
// non-blank line, in place. Blank lines are left empty.
func dedentBlock(lines []string) {
	indent := -1
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		if n := len(line) - len(trimmed); indent < 0 || n < indent {
			indent = n
		}
	}
	if indent <= 0 {
		return
	}
	for i, line := range lines {
		if len(line) >= indent {
			lines[i] = line[indent:]
		} else {
			lines[i] = ""
		}
	}
}

// isPragmaBody reports whether a `//` comment body is a compiler directive
// rather than prose. Covers the forms the transpiler itself emits (see
// insertLineDirectives and insertEmbedDirectives), Go's own `//go:` family,
// which GALA source may carry through to the generated output, and the legacy
// `// +build` constraint. A `+build` line is never prose: carried into the
// generated Go as documentation, gofmt would turn it into a `//go:build` header
// and silently drop the whole file from the build.
func isPragmaBody(body string) bool {
	return strings.HasPrefix(body, "go:") ||
		body == "line" ||
		strings.HasPrefix(body, "line ") ||
		constraint.IsPlusBuild("//"+body)
}
