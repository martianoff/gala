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

import "strings"

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
}

// Split parses the CONTENT of an interpolated/format string — i.e. the text
// between the opening `s"`/`f"` and the closing `"` — into ordered parts.
// Handles `$identifier`, `${expression}`, and `$$` (literal `$`); for format
// strings it also captures a `%spec` following an embedded expression.
func Split(content string) []Part {
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
				parts = append(parts, Part{IsLiteral: true, Text: literal.String(), Offset: litStart})
				literal.Reset()
			}

			j := EndOfEmbeddedExpr(content, i)

			// exprStart is the byte after `${`, i.e. where the expression
			// source itself begins.
			exprStart := i + 2
			exprText := unescapeExpr(content[exprStart:j])
			j++ // skip closing }

			fmtSpec := ""
			if j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, Part{IsLiteral: false, Text: exprText, FormatSpec: fmtSpec, Offset: exprStart})
			i = j
			continue
		}

		// $identifier
		if isIdentStart(next) {
			if literal.Len() > 0 {
				parts = append(parts, Part{IsLiteral: true, Text: literal.String(), Offset: litStart})
				literal.Reset()
			}

			// identStart is the byte after `$`, where the name begins.
			identStart := i + 1
			j := identStart
			for j < len(content) && isIdentPart(content[j]) {
				j++
			}

			identName := content[identStart:j]

			fmtSpec := ""
			if j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, Part{IsLiteral: false, Text: identName, FormatSpec: fmtSpec, Offset: identStart})
			i = j
			continue
		}

		// $ followed by something else — treat as literal $.
		writeLit(i, '$')
		i++
	}

	if literal.Len() > 0 {
		parts = append(parts, Part{IsLiteral: true, Text: literal.String(), Offset: litStart})
	}

	return parts
}

// EndOfEmbeddedExpr locates the `}` that closes the `${` beginning at
// content[dollar], counting brace nesting and skipping escaped characters so a
// brace written inside a nested literal does not close the block early. It
// returns the index of that `}`, or len(content) when the block is unterminated.
//
// Split walks embedded expressions with it, and so does the transformer's
// escape validation, which must skip exactly the regions Split treats as
// expression source rather than literal text. Sharing the walk is what makes
// "exactly" true.
func EndOfEmbeddedExpr(content string, dollar int) int {
	depth := 1
	j := dollar + 2
	for j < len(content) && depth > 0 {
		switch {
		case content[j] == '{':
			depth++
		case content[j] == '}':
			depth--
		case content[j] == '\\' && j+1 < len(content):
			j++ // skip escaped char
		}
		if depth > 0 {
			j++
		}
	}
	return j
}

// unescapeExpr converts escaped quotes inside `${}` expression blocks back to
// regular quotes so the expression can be re-parsed by the GALA parser.
func unescapeExpr(expr string) string {
	return strings.ReplaceAll(expr, `\"`, `"`)
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
