package transformer

import (
	"fmt"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
)

// String escape validation
// ------------------------
// GALA's lexer accepts `'\\' .` — ANY character after a backslash — inside
// `"..."`, `'x'`, `s"..."` and `f"..."` literals. That is strictly wider than
// the escape set Go accepts, and the transformer copies a literal's raw text
// verbatim into the generated `*ast.BasicLit`. An unrecognised escape (e.g.
// `"(\d{4})"`, a regular expression written without doubling the backslash)
// therefore travelled untouched into the emitted Go, where it is also invalid.
//
// Nothing downstream caught it: go/printer prints a BasicLit without parsing
// it, and both the generator's canonicalising `format.Source` pass and the
// source-map marker rewrite treated "the buffer does not re-parse" as a
// tolerable condition and fell back to the unformatted text. The result was
// unusable Go emitted with a success exit code.
//
// Validating here — at the single point where a GALA literal token becomes a Go
// literal — turns that into a framed diagnostic that points at the offending
// escape in the `.gala` source. Backtick raw strings have no escapes at all and
// are deliberately not scanned.

// escapeQuote selects which quote-escape is legal for the literal being
// scanned: Go allows `\"` only in a string literal and `\'` only in a rune
// literal.
type escapeQuote byte

const (
	escapeInString escapeQuote = '"'
	escapeInRune   escapeQuote = '\''
)

// badEscape describes the first invalid escape sequence found in a literal.
type badEscape struct {
	// Offset is the byte offset of the backslash within the scanned text.
	Offset int
	// Seq is the offending sequence exactly as written in the source.
	Seq string
	// Reason is a specific defect ("\\x requires exactly 2 hexadecimal
	// digits"); empty when the escape character is simply not recognised.
	Reason string
}

// validEscapeList renders the accepted escape set for a diagnostic hint.
func validEscapeList(q escapeQuote) string {
	quoted := `\"`
	if q == escapeInRune {
		quoted = `\'`
	}
	return `\a \b \f \n \r \t \v \\ ` + quoted + ` \xHH \uHHHH \UHHHHHHHH and \OOO (octal)`
}

// classifyEscape inspects the escape sequence starting at the backslash at
// text[i]. It returns the sequence's byte length and, when the sequence is
// invalid, a populated badEscape with ok=false.
func classifyEscape(text string, i int, q escapeQuote) (length int, bad badEscape, ok bool) {
	// A trailing backslash cannot occur in a lexed literal (the grammar's
	// `'\\' .` always consumes a following character), but guard anyway.
	if i+1 >= len(text) {
		return 1, badEscape{Offset: i, Seq: `\`, Reason: "a trailing backslash escapes nothing"}, false
	}

	c := text[i+1]
	switch c {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\':
		return 2, badEscape{}, true
	case '"':
		if q == escapeInString {
			return 2, badEscape{}, true
		}
		return 2, badEscape{Offset: i, Seq: `\"`, Reason: `\" is only valid in a string literal; inside a rune literal write "`}, false
	case '\'':
		if q == escapeInRune {
			return 2, badEscape{}, true
		}
		return 2, badEscape{Offset: i, Seq: `\'`, Reason: `\' is only valid in a rune literal; inside a string literal write '`}, false
	case 'x':
		return hexEscape(text, i, 2, `\x`)
	case 'u':
		return hexEscape(text, i, 4, `\u`)
	case 'U':
		return hexEscape(text, i, 8, `\U`)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		return octalEscape(text, i)
	}

	// Unrecognised escape character. Report the two-byte sequence, taking the
	// whole rune when the offending character is multi-byte.
	_, size := utf8.DecodeRuneInString(text[i+1:])
	return 1 + size, badEscape{Offset: i, Seq: text[i : i+1+size]}, false
}

// hexEscape validates `\xHH`, `\uHHHH` or `\UHHHHHHHH`: exactly n hexadecimal
// digits, and — for the Unicode forms — a code point Go can actually encode.
func hexEscape(text string, i, n int, form string) (int, badEscape, bool) {
	digits := 0
	value := 0
	for digits < n && i+2+digits < len(text) {
		d, isHex := hexDigit(text[i+2+digits])
		if !isHex {
			break
		}
		value = value*16 + d
		digits++
	}
	seq := text[i : i+2+digits]
	if digits < n {
		return 2 + digits, badEscape{
			Offset: i,
			Seq:    seq,
			Reason: fmt.Sprintf("%s requires exactly %d hexadecimal digits", form, n),
		}, false
	}
	length := 2 + n
	if form == `\x` {
		return length, badEscape{}, true
	}
	if value >= 0xD800 && value <= 0xDFFF {
		return length, badEscape{
			Offset: i,
			Seq:    seq,
			Reason: "a surrogate half (U+D800-U+DFFF) is not a valid Unicode code point",
		}, false
	}
	if value > 0x10FFFF {
		return length, badEscape{
			Offset: i,
			Seq:    seq,
			Reason: "the value is above the maximum Unicode code point U+10FFFF",
		}, false
	}
	return length, badEscape{}, true
}

// octalEscape validates `\OOO`: exactly three octal digits denoting a byte.
func octalEscape(text string, i int) (int, badEscape, bool) {
	digits := 0
	value := 0
	for digits < 3 && i+1+digits < len(text) {
		c := text[i+1+digits]
		if c < '0' || c > '7' {
			break
		}
		value = value*8 + int(c-'0')
		digits++
	}
	seq := text[i : i+1+digits]
	if digits < 3 {
		return 1 + digits, badEscape{
			Offset: i,
			Seq:    seq,
			Reason: `a backslash followed by a digit starts an octal escape, which requires exactly 3 octal digits (0-7)`,
		}, false
	}
	if value > 255 {
		return 4, badEscape{
			Offset: i,
			Seq:    seq,
			Reason: fmt.Sprintf("the octal value %d is above the maximum byte value 255", value),
		}, false
	}
	return 4, badEscape{}, true
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// firstInvalidEscape scans a plain string or rune literal's raw text (quotes
// included) and returns the first invalid escape sequence in it.
func firstInvalidEscape(text string, q escapeQuote) (badEscape, bool) {
	for i := 0; i < len(text); {
		if text[i] != '\\' {
			i++
			continue
		}
		length, bad, ok := classifyEscape(text, i, q)
		if !ok {
			return bad, true
		}
		i += length
	}
	return badEscape{}, false
}

// firstInvalidEscapeInterp scans an interpolated (`s"…"`) or format (`f"…"`)
// literal's raw text. Escapes inside a `${…}` block belong to the embedded GALA
// expression — that expression is re-parsed and its own literals are validated
// through the normal path — so those regions are skipped here, using the same
// brace-nesting walk as interpolation.Split so the two agree on where an
// embedded expression starts and ends.
func firstInvalidEscapeInterp(text string) (badEscape, bool) {
	for i := 0; i < len(text); {
		switch {
		case text[i] == '\\':
			length, bad, ok := classifyEscape(text, i, escapeInString)
			if !ok {
				return bad, true
			}
			i += length
		case text[i] == '$' && i+1 < len(text) && text[i+1] == '{':
			depth := 1
			j := i + 2
			for j < len(text) && depth > 0 {
				switch {
				case text[j] == '{':
					depth++
				case text[j] == '}':
					depth--
				case text[j] == '\\' && j+1 < len(text):
					j++
				}
				if depth > 0 {
					j++
				}
			}
			i = j + 1
		default:
			i++
		}
	}
	return badEscape{}, false
}

// literalEscapeKind identifies the literal form being scanned: it selects the
// scanner (plain vs. interpolation-aware), the legal quote escape, and the
// noun used in the diagnostic.
type literalEscapeKind struct {
	noun   string
	quote  escapeQuote
	interp bool
}

var (
	escapeKindString   = literalEscapeKind{noun: "string literal", quote: escapeInString}
	escapeKindRune     = literalEscapeKind{noun: "rune literal", quote: escapeInRune}
	escapeKindInterp   = literalEscapeKind{noun: `interpolated string literal (s"...")`, quote: escapeInString, interp: true}
	escapeKindFormat   = literalEscapeKind{noun: `format string literal (f"...")`, quote: escapeInString, interp: true}
	escapeKindFieldTag = literalEscapeKind{noun: "struct field tag", quote: escapeInString}
)

// checkLiteralEscapes validates the escape sequences in a literal token and
// returns a framed, `.gala`-positioned diagnostic for the first invalid one.
// node is the terminal for the literal token. Returns nil when every escape is
// valid.
func (t *galaASTTransformer) checkLiteralEscapes(node antlr.TerminalNode, kind literalEscapeKind) error {
	if node == nil {
		return nil
	}
	text := node.GetText()
	q := kind.quote
	var (
		bad badEscape
		hit bool
	)
	if kind.interp {
		bad, hit = firstInvalidEscapeInterp(text)
	} else {
		bad, hit = firstInvalidEscape(text, q)
	}
	if !hit {
		return nil
	}

	// Quote the sequence as the user WROTE it. %q would re-escape the
	// backslash, so a source `\d` would be reported as "\\d" — the very
	// confusion this diagnostic exists to clear up.
	msg := fmt.Sprintf(`invalid escape sequence "%s" in %s`, bad.Seq, kind.noun)
	if bad.Reason != "" {
		msg += ": " + bad.Reason
	}
	hint := "valid escapes are " + validEscapeList(q) +
		"; to write a literal backslash (in a regular expression, for example) double it (\\\\) or use a backtick raw string"

	// ANTLR reports positions as 0-based rune indices, so convert the byte
	// offset of the escape within the token into a rune offset before adding
	// it to the token's column. GALA string, rune and interpolated literals
	// cannot span lines, so the line is the token's own.
	tok := node.GetSymbol()
	startCol := tok.GetColumn() + utf8.RuneCountInString(text[:bad.Offset])
	endCol := startCol + utf8.RuneCountInString(bad.Seq)

	err := galaerr.NewCodedSemanticError(
		galaerr.CodeInvalidStringEscape,
		tok.GetLine(),
		startCol,
		msg,
		hint,
	).WithSpan(endCol)
	err.FilePath = t.filePath
	return err
}
