// Package interpolation splits the body of a GALA interpolated (`s"…"`) or
// format (`f"…"`) string literal into its literal-text and embedded-expression
// parts.
//
// It is a pure lexer over the interp-body mini-syntax — `$identifier`,
// `${ expression }`, `$$` (a literal `$`), and, for format strings, a trailing
// `%spec` after an embedded expression. It carries no transpiler or parser
// state, so it is shared by:
//
//   - the transformer, which transpiles each embedded expression into a
//     fmt.Sprintf argument, and
//   - the concurrency capture analysis, which walks each embedded expression
//     for free variables.
//
// Both consumers therefore agree, by construction, on exactly what an
// interpolation embeds.
package interpolation

import (
	"strings"
	"unicode/utf8"
)

// Part is one piece of an interpolated string: either literal text or an
// embedded expression (with its optional explicit format spec).
type Part struct {
	// IsLiteral is true for literal text, false for an embedded expression.
	IsLiteral bool
	// Text is the literal text (still escaped as written) or, for an embedded
	// expression, the GALA expression source (with `\"` unescaped so it re-parses).
	Text string
	// FormatSpec is an explicit printf spec that followed an embedded expression
	// in a format string (e.g. "%04d"); empty otherwise.
	FormatSpec string
	// Offset is the BYTE index, within the content passed to Split, at which
	// this part's Text begins — for `${expr}` the byte after the `{`, and for
	// `$ident` the byte after the `$`. It lets a caller that knows where the
	// host string literal sits map a position inside an embedded expression
	// back to a real source line and column, so a diagnostic raised while
	// transforming that expression points at the code the user wrote.
	//
	// Exactness caveat: for an embedded expression Text is unescaped (`\"` →
	// `"`), so Offset locates the expression's START exactly, while positions
	// further into an expression that contains an escaped quote drift left by
	// one byte per escape. Literal parts carry the offset of their first byte.
	Offset int
	// Start and End delimit the part's source within the content: a literal
	// run as written (`$$` included), or an embedded expression from its `$`
	// to just past its name or closing `}`. A format spec after it is not
	// included: it starts at End.
	Start, End int
}

// PrintedByString reports whether fmt prints the embedded expression p as
// it prints an operand of Println, by a String it has: with no spec, or with
// a %v or %s one that is not Go syntax (`%#v`).
func (p Part) PrintedByString() bool {
	spec := p.FormatSpec
	if spec == "" {
		return true
	}
	verb := spec[len(spec)-1]
	return (verb == 'v' || verb == 's') && !strings.Contains(spec, "#")
}

// Split parses the CONTENT of an interpolated/format string — i.e. the text
// between the opening `s"`/`f"` and the closing `"` — into ordered parts.
// Handles `$identifier`, `${expression}`, and `$$` (literal `$`). With
// formatSpecs, for an `f"..."` string, it also captures a `%spec` following
// an embedded expression; in an `s"..."` string a `%` is literal text.
func Split(content string, formatSpecs bool) []Part {
	var parts []Part
	var literal strings.Builder
	i := 0
	// litStart is the byte offset at which the literal run currently being
	// accumulated began; it is refreshed every time the builder is empty and
	// about to take its first byte, so a flushed literal part reports where it
	// actually started rather than where it ended.
	litStart := 0
	writeLit := func(at int, b byte) {
		if literal.Len() == 0 {
			litStart = at
		}
		literal.WriteByte(b)
	}

	for i < len(content) {
		if content[i] == '\\' && i+1 < len(content) {
			// Escape sequence — pass through as-is.
			writeLit(i, content[i])
			writeLit(i+1, content[i+1])
			i += 2
			continue
		}

		if content[i] != '$' {
			writeLit(i, content[i])
			i++
			continue
		}

		// Found $
		if i+1 >= len(content) {
			writeLit(i, '$')
			i++
			continue
		}

		next := content[i+1]

		// $$ → literal $
		if next == '$' {
			writeLit(i, '$')
			i += 2
			continue
		}

		// ${expr} or ${expr}%spec
		if next == '{' {
			if literal.Len() > 0 {
				parts = append(parts, Part{IsLiteral: true, Text: literal.String(), Offset: litStart, Start: litStart, End: i})
				literal.Reset()
			}

			j := EndOfEmbeddedExpr(content, i)

			// exprStart is the byte after `${`, i.e. where the expression
			// source itself begins.
			exprStart := i + 2
			exprText := unescapeExpr(content[exprStart:j])
			j = min(j+1, len(content)) // skip closing }
			end := j

			fmtSpec := ""
			if formatSpecs && j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, Part{IsLiteral: false, Text: exprText, FormatSpec: fmtSpec, Offset: exprStart, Start: i, End: end})
			i = j
			continue
		}

		// $identifier
		if isIdentStart(next) {
			if literal.Len() > 0 {
				parts = append(parts, Part{IsLiteral: true, Text: literal.String(), Offset: litStart, Start: litStart, End: i})
				literal.Reset()
			}

			// identStart is the byte after `$`, where the name begins.
			identStart := i + 1
			j := identStart
			for j < len(content) && isIdentPart(content[j]) {
				j++
			}

			identName := content[identStart:j]
			end := j

			fmtSpec := ""
			if formatSpecs && j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, Part{IsLiteral: false, Text: identName, FormatSpec: fmtSpec, Offset: identStart, Start: i, End: end})
			i = j
			continue
		}

		// $ followed by something else — treat as literal $.
		writeLit(i, '$')
		i++
	}

	if literal.Len() > 0 {
		parts = append(parts, Part{IsLiteral: true, Text: literal.String(), Offset: litStart, Start: litStart, End: i})
	}

	return parts
}

// Position is the line and column of content[offset], given that content
// starts at line/col of its file. Columns count runes (what diagnostics index
// by) and restart at 0 after a newline.
func Position(content string, offset, line, col int) (int, int) {
	if offset < 0 || offset > len(content) {
		return line, col
	}
	prefix := content[:offset]
	if nl := strings.LastIndexByte(prefix, '\n'); nl >= 0 {
		return line + strings.Count(prefix, "\n"), utf8.RuneCountInString(prefix[nl+1:])
	}
	return line, col + utf8.RuneCountInString(prefix)
}

// EndOfEmbeddedExpr locates the `}` that closes the `${` beginning at
// content[dollar]. It returns the index of that `}`, or len(content) when the
// block is unterminated.
//
// It walks the block as the lexer's BRACE_CONTENT rule in gala.g4 does, so the
// two agree on where every embedded expression ends: a `\` escapes the
// character after it, a `"`-quoted string literal is opaque (a brace in
// `"}"` neither nests nor closes), and every other `{` and `}` nests.
//
// Split walks embedded expressions with it, and so does the transformer's
// escape validation, which must skip exactly the regions Split treats as
// expression source rather than literal text. Sharing the walk is what makes
// "exactly" true.
func EndOfEmbeddedExpr(content string, dollar int) int {
	depth := 1
	for j := dollar + 2; j < len(content); j++ {
		switch content[j] {
		case '\\':
			j++ // skip the escaped character
		case '"':
			j = endOfStringLiteral(content, j)
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(content)
}

// endOfStringLiteral is the index of the `"` that closes the string literal
// whose opening `"` is content[open], skipping the literal's escapes, or
// len(content) when it is unterminated.
func endOfStringLiteral(content string, open int) int {
	for j := open + 1; j < len(content); j++ {
		switch content[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return len(content)
}

// unescapeExpr turns each `\"` in an embedded expression back into `"`, so an
// expression written with escaped quotes (`${f(\"x\")}`) re-parses. It reads
// escapes in pairs, as the lexer does, so any other escape is kept whole: the
// `\\` in `"\\"` stays an escaped backslash rather than lending its second `\`
// to the closing quote.
func unescapeExpr(expr string) string {
	var b strings.Builder
	b.Grow(len(expr))
	for i := 0; i < len(expr); i++ {
		if expr[i] == '\\' && i+1 < len(expr) {
			i++
			if expr[i] != '"' {
				b.WriteByte('\\')
			}
		}
		b.WriteByte(expr[i])
	}
	return b.String()
}

// extractFormatSpec extracts a Go printf format specifier starting at position i
// (which points to `%`). Returns the spec string and the position after it, or
// ("", i) when there is no valid verb (so the `%` is a literal).
func extractFormatSpec(content string, i int) (string, int) {
	if i >= len(content) || content[i] != '%' {
		return "", i
	}

	j := i + 1 // skip %

	// Flags: +, -, #, ' ', 0
	for j < len(content) {
		c := content[j]
		if c == '+' || c == '-' || c == '#' || c == ' ' || c == '0' {
			j++
		} else {
			break
		}
	}

	// Width: digits or *
	if j < len(content) && content[j] == '*' {
		j++
	} else {
		for j < len(content) && content[j] >= '0' && content[j] <= '9' {
			j++
		}
	}

	// Precision: .digits or .*
	if j < len(content) && content[j] == '.' {
		j++
		if j < len(content) && content[j] == '*' {
			j++
		} else {
			for j < len(content) && content[j] >= '0' && content[j] <= '9' {
				j++
			}
		}
	}

	// Verb
	if j < len(content) && isFormatVerb(content[j]) {
		j++
		return content[i:j], j
	}

	return "", i
}

// isFormatVerb reports whether c is a valid Go printf verb character.
func isFormatVerb(c byte) bool {
	switch c {
	case 'b', 'c', 'd', 'e', 'E', 'f', 'F', 'g', 'G', 'o', 'O', 'p', 'q', 's', 't', 'T', 'U', 'v', 'x', 'X':
		return true
	}
	return false
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
