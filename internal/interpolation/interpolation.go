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
}

// Split parses the CONTENT of an interpolated/format string — i.e. the text
// between the opening `s"`/`f"` and the closing `"` — into ordered parts.
// Handles `$identifier`, `${expression}`, and `$$` (literal `$`); for format
// strings it also captures a `%spec` following an embedded expression.
func Split(content string) []Part {
	var parts []Part
	var literal strings.Builder
	i := 0

	for i < len(content) {
		if content[i] == '\\' && i+1 < len(content) {
			// Escape sequence — pass through as-is.
			literal.WriteByte(content[i])
			literal.WriteByte(content[i+1])
			i += 2
			continue
		}

		if content[i] != '$' {
			literal.WriteByte(content[i])
			i++
			continue
		}

		// Found $
		if i+1 >= len(content) {
			literal.WriteByte('$')
			i++
			continue
		}

		next := content[i+1]

		// $$ → literal $
		if next == '$' {
			literal.WriteByte('$')
			i += 2
			continue
		}

		// ${expr} or ${expr}%spec
		if next == '{' {
			if literal.Len() > 0 {
				parts = append(parts, Part{IsLiteral: true, Text: literal.String()})
				literal.Reset()
			}

			// Find matching } with brace nesting.
			braceDepth := 1
			j := i + 2
			for j < len(content) && braceDepth > 0 {
				if content[j] == '{' {
					braceDepth++
				} else if content[j] == '}' {
					braceDepth--
				} else if content[j] == '\\' && j+1 < len(content) {
					j++ // skip escaped char
				}
				if braceDepth > 0 {
					j++
				}
			}

			exprText := unescapeExpr(content[i+2 : j])
			j++ // skip closing }

			fmtSpec := ""
			if j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, Part{IsLiteral: false, Text: exprText, FormatSpec: fmtSpec})
			i = j
			continue
		}

		// $identifier
		if isIdentStart(next) {
			if literal.Len() > 0 {
				parts = append(parts, Part{IsLiteral: true, Text: literal.String()})
				literal.Reset()
			}

			j := i + 1
			for j < len(content) && isIdentPart(content[j]) {
				j++
			}

			identName := content[i+1 : j]

			fmtSpec := ""
			if j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, Part{IsLiteral: false, Text: identName, FormatSpec: fmtSpec})
			i = j
			continue
		}

		// $ followed by something else — treat as literal $.
		literal.WriteByte('$')
		i++
	}

	if literal.Len() > 0 {
		parts = append(parts, Part{IsLiteral: true, Text: literal.String()})
	}

	return parts
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
