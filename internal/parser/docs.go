package parser

import (
	"go/build/constraint"
	"strings"

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
// grammar is unaffected. nativeDocComments walks the token stream after the
// lexer and pairs each comment run with the declaration it precedes. This
// costs one linear token walk over tokens the parse lexes anyway, so no extra
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
// column (from 0, counted in code points), as diagnostics report a position.
// The native tree's offsets count bytes, so doc comments are keyed by the
// position instead, which every reader of a declaration has.
type DocKey struct {
	Line, Column int
}

// Docs is a file's doc comments, by the start of the declaration each one
// documents.
type Docs = map[DocKey]string

// nativeDocComments pairs the comment runs of ts, galop's native tokens
// (hidden ones included), with the declarations they document. It returns
// nil for a source with no doc comments.
func nativeDocComments(ts *native.GalaTokens) Docs {
	run := newDocRun()
	// A cursor over Hidden rather than ts.HiddenBefore(i), which would binary
	// search once per token. Texts are read with ts.Text and ts.HiddenText:
	// for a source that is not valid UTF-8 they give U+FFFD for each invalid
	// byte, as ANTLR reads it, where the token spans index the raw bytes.
	h := 0
	for i, tk := range ts.Tokens {
		if tk.Type == native.GalaEOF {
			break
		}
		// The line the previous token ends on, computed only once a comment
		// needs it: all but a handful of tokens are followed by none.
		prevCodeEndLine := -1
		for ; h < len(ts.Hidden) && int(ts.Hidden[h].Next) == i; h++ {
			if i > 0 && prevCodeEndLine < 0 {
				prevCodeEndLine = int(ts.Tokens[i-1].Line) + strings.Count(ts.Text(i-1), "\n")
			}
			run.comment(int(ts.Hidden[h].Line), prevCodeEndLine, ts.HiddenText(h))
		}
		run.code(int(tk.Line), func() int { return int(tk.Column) })
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
// when there is none. raw is the comment's text.
func (r *docRun) comment(startLine, prevCodeEndLine int, raw string) {
	if startLine == prevCodeEndLine {
		return // trailing comment on a line that already had code
	}
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

// code ends the pending run at a default-channel token that starts on line:
// the run documents it when it ends on the line just above. column, the
// token's column, is read only for a token the run documents.
func (r *docRun) code(line int, column func() int) {
	if len(r.block) > 0 && line == r.blockEndLine+1 {
		if r.docs == nil {
			r.docs = make(Docs)
		}
		r.docs[DocKey{Line: line, Column: column()}] = strings.Join(r.block, "\n")
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
// `// +build` constraint (a GALA file that holds a directive, as
// isGoDirectiveBody reads one, is GALA-E0119, but an editor still reads its
// docs; any other `//go:` line is kept out of the docs too). A `+build` line is never
// prose: carried into the generated Go as documentation, gofmt would turn it
// into a `//go:build` header and silently drop the whole file from the build.
func isPragmaBody(body string) bool {
	return strings.HasPrefix(body, "go:") ||
		isGoDirectiveBody(body) ||
		body == "line" ||
		strings.HasPrefix(body, "line ")
}
