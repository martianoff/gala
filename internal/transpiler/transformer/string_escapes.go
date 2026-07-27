package transformer

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/interpolation"
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

// escapeLength reports the byte length of the escape sequence starting at the
// backslash at text[i], and whether Go accepts that sequence.
//
// The accept/reject decision is Go's own: strconv.UnquoteChar decodes exactly
// the escape set an interpreted Go literal admits — the same digit counts,
// surrogate rejection and value ranges — so GALA's set stays identical to Go's
// by construction instead of by a hand-kept table that could drift from it.
// explainEscape below only has to describe a rejection, never make it.
func escapeLength(text string, i int, q escapeQuote) (int, bool) {
	_, _, tail, err := strconv.UnquoteChar(text[i:], byte(q))
	if err != nil {
		return 0, false
	}
	return len(text) - i - len(tail), true
}

// explainEscape describes the escape sequence at text[i], which escapeLength
// has already rejected: the sequence exactly as written (so the caret can span
// it) and, where the defect is a specific one, the reason.
func explainEscape(text string, i int, q escapeQuote) badEscape {
	// A trailing backslash cannot occur in a lexed literal (the grammar's
	// `'\\' .` always consumes a following character), but guard anyway.
	if i+1 >= len(text) {
		return badEscape{Offset: i, Seq: `\`, Reason: "a trailing backslash escapes nothing"}
	}

	// Only the quote escape that does NOT match the literal being scanned can
	// reach here: `\"` is valid in a string and `\'` in a rune, so whichever
	// arrives was rejected for being written in the other one.
	switch text[i+1] {
	case '"':
		return badEscape{Offset: i, Seq: `\"`, Reason: `\" is only valid in a string literal; inside a rune literal write "`}
	case '\'':
		return badEscape{Offset: i, Seq: `\'`, Reason: `\' is only valid in a rune literal; inside a string literal write '`}
	case 'x':
		return explainHexEscape(text, i, 2, `\x`)
	case 'u':
		return explainHexEscape(text, i, 4, `\u`)
	case 'U':
		return explainHexEscape(text, i, 8, `\U`)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		return explainOctalEscape(text, i)
	}

	// Unrecognised escape character. Report the two-byte sequence, taking the
	// whole rune when the offending character is multi-byte.
	_, size := utf8.DecodeRuneInString(text[i+1:])
	return badEscape{Offset: i, Seq: text[i : i+1+size]}
}

// explainHexEscape explains a rejected `\xHH`, `\uHHHH` or `\UHHHHHHHH`: either
// it does not carry n hexadecimal digits, or — for the Unicode forms — its
// value is not a code point Go can encode.
func explainHexEscape(text string, i, n int, form string) badEscape {
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
	bad := badEscape{Offset: i, Seq: text[i : i+2+digits]}
	// Every arm is a POSITIVE test, deliberately — there is no `default`.
	//
	// `\x` denotes a raw byte, not a code point, so neither code-point arm can
	// apply to it: two hex digits max out at 0xFF, well below both the surrogate
	// range and U+10FFFF. A `default` arm would therefore be unreachable for
	// `\x` (its only possible rejection is "too few digits", the first arm) yet
	// would label a byte escape "above the maximum Unicode code point U+10FFFF"
	// if it ever were reached. Leaving Reason empty in that impossible case
	// degrades to the bare "invalid escape sequence" message, which is vague but
	// never wrong.
	switch {
	case digits < n:
		bad.Reason = fmt.Sprintf("%s requires exactly %d hexadecimal digits", form, n)
	case value >= 0xD800 && value <= 0xDFFF:
		bad.Reason = "a surrogate half (U+D800-U+DFFF) is not a valid Unicode code point"
	case value > 0x10FFFF:
		bad.Reason = "the value is above the maximum Unicode code point U+10FFFF"
	}
	return bad
}

// explainOctalEscape explains a rejected `\OOO`: either it does not carry three
// octal digits, or the byte they denote is out of range.
func explainOctalEscape(text string, i int) badEscape {
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
	bad := badEscape{Offset: i, Seq: text[i : i+1+digits]}
	if digits < 3 {
		bad.Reason = `a backslash followed by a digit starts an octal escape, which requires exactly 3 octal digits (0-7)`
	} else {
		bad.Reason = fmt.Sprintf("the octal value %d is above the maximum byte value 255", value)
	}
	return bad
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
		length, ok := escapeLength(text, i, q)
		if !ok {
			return explainEscape(text, i, q), true
		}
		i += length
	}
	return badEscape{}, false
}

// firstInvalidEscapeInterp scans an interpolated (`s"…"`) or format (`f"…"`)
// literal's raw text. Escapes inside a `${…}` block belong to the embedded GALA
// expression — that expression is re-parsed and its own literals are validated
// through the normal path — so those regions are skipped here, using
// interpolation.EndOfEmbeddedExpr, the same walk interpolation.Split uses, so
// the two cannot disagree on where an embedded expression ends.
func firstInvalidEscapeInterp(text string) (badEscape, bool) {
	for i := 0; i < len(text); {
		switch {
		case text[i] == '\\':
			length, ok := escapeLength(text, i, escapeInString)
			if !ok {
				return explainEscape(text, i, escapeInString), true
			}
			i += length
		case text[i] == '$' && i+1 < len(text) && text[i+1] == '{':
			i = interpolation.EndOfEmbeddedExpr(text, i) + 1
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
