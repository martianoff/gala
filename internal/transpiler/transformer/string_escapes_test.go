package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// String literal escape validation
// --------------------------------
// GALA's lexer accepts a backslash followed by ANY character, and the
// transformer copies a literal's raw text verbatim into the generated Go
// literal. An escape Go does not recognise — `"(\d{4})"`, a regular expression
// whose backslash was not doubled, is the canonical offender — therefore used
// to travel untouched into the emitted Go, which then did not compile. Worse,
// the unparseable buffer defeated the two downstream passes that re-parse it
// (the generator's canonicalising format.Source and the source-map marker
// rewrite), both of which silently returned their input unchanged, so the
// transpiler emitted broken Go — complete with raw `__gala_line_*` internal
// markers — and reported success.
//
// These tests pin the two halves of the fix: the literal is now rejected at its
// GALA source position with GALA-E0038, and every escape Go does accept still
// transpiles unchanged.

func newEscapeTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestInvalidStringEscapeRejected covers the negative path: each malformed
// escape must fail transpilation with GALA-E0038, positioned at the offending
// escape in the .gala source rather than anywhere in the generated Go.
func TestInvalidStringEscapeRejected(t *testing.T) {
	trans := newEscapeTranspiler()

	cases := []struct {
		name string
		// body is spliced into main() as a single statement on GALA line 4.
		body string
		// wantMsg is a distinctive fragment of the rendered message.
		wantMsg string
		// wantCol is the 0-based column of the offending backslash.
		wantCol int
	}{
		{
			name:    "unknown escape backslash-d in a regular expression",
			body:    `    val pattern = "(\d{4})"`,
			wantMsg: `invalid escape sequence "\d" in string literal`,
			wantCol: 20,
		},
		{
			name:    "unknown escape backslash-e",
			body:    `    val s = "\e[0m"`,
			wantMsg: `invalid escape sequence "\e" in string literal`,
			wantCol: 13,
		},
		{
			name:    "backslash-x with too few hex digits",
			body:    `    val s = "\x4"`,
			wantMsg: `invalid escape sequence "\x4" in string literal: \x requires exactly 2 hexadecimal digits`,
			wantCol: 13,
		},
		{
			name:    "backslash-x with a non-hex digit",
			body:    `    val s = "\xZZ"`,
			wantMsg: `invalid escape sequence "\x" in string literal: \x requires exactly 2 hexadecimal digits`,
			wantCol: 13,
		},
		{
			name:    "backslash-u with too few hex digits",
			body:    `    val s = "\u12"`,
			wantMsg: `invalid escape sequence "\u12" in string literal: \u requires exactly 4 hexadecimal digits`,
			wantCol: 13,
		},
		{
			name:    "backslash-U with too few hex digits",
			body:    `    val s = "\U0001F60"`,
			wantMsg: `\U requires exactly 8 hexadecimal digits`,
			wantCol: 13,
		},
		{
			name:    "surrogate half is not a code point",
			body:    `    val s = "\uD800"`,
			wantMsg: `a surrogate half (U+D800-U+DFFF) is not a valid Unicode code point`,
			wantCol: 13,
		},
		{
			name:    "code point above the Unicode maximum",
			body:    `    val s = "\U0011FFFF"`,
			wantMsg: `the value is above the maximum Unicode code point U+10FFFF`,
			wantCol: 13,
		},
		{
			name:    "octal escape with too few digits",
			body:    `    val s = "\12"`,
			wantMsg: `requires exactly 3 octal digits (0-7)`,
			wantCol: 13,
		},
		{
			name:    "octal escape above the maximum byte value",
			body:    `    val s = "\400"`,
			wantMsg: `the octal value 256 is above the maximum byte value 255`,
			wantCol: 13,
		},
		{
			name:    "single quote is not escaped in a string literal",
			body:    `    val s = "it\'s"`,
			wantMsg: `\' is only valid in a rune literal; inside a string literal write '`,
			wantCol: 15,
		},
		{
			name:    "invalid escape in an interpolated string literal",
			body:    `    val s = s"pattern \d ok"`,
			wantMsg: `invalid escape sequence "\d" in interpolated string literal (s"...")`,
			wantCol: 22,
		},
		{
			name:    "invalid escape in a format string literal",
			body:    `    val s = f"pattern \d ok"`,
			wantMsg: `invalid escape sequence "\d" in format string literal (f"...")`,
			wantCol: 22,
		},
		{
			name:    "invalid escape in a rune literal",
			body:    `    val c = '\d'`,
			wantMsg: `invalid escape sequence "\d" in rune literal`,
			wantCol: 13,
		},
		{
			name:    "double quote is not escaped in a rune literal",
			body:    `    val c = '\"'`,
			wantMsg: `\" is only valid in a string literal; inside a rune literal write "`,
			wantCol: 13,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc main() {\n" + tc.body + "\n}\n"

			_, err := trans.Transpile(src, "escapes.gala")
			require.Error(t, err, "an invalid escape must fail transpilation")

			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, galaerr.CodeInvalidStringEscape, se.Code)
			assert.Contains(t, se.Error(), tc.wantMsg)

			// The diagnostic must point at the .gala source, never at
			// generated Go.
			assert.Equal(t, "escapes.gala", se.FilePath)
			assert.Equal(t, 4, se.Line, "the offending literal is on GALA line 4")
			assert.Equal(t, tc.wantCol, se.Column,
				"the caret must sit on the offending backslash")
			assert.True(t, se.HasSpan(),
				"the diagnostic should carry an exact span for the escape")

			// The hint must name the accepted escapes and the backslash fix.
			assert.Contains(t, se.Hint, `\xHH \uHHHH \UHHHHHHHH`)
		})
	}
}

// TestInvalidEscapeInMatchPatternRejected covers the other expression position a
// string literal reaches: a match pattern.
func TestInvalidEscapeInMatchPatternRejected(t *testing.T) {
	trans := newEscapeTranspiler()

	src := "package main\n\nfunc classify(s string) int = s match {\n" +
		"    case \"\\d\" => 1\n" +
		"    case _ => 0\n" +
		"}\n\nfunc main() {\n    Println(classify(\"x\"))\n}\n"

	_, err := trans.Transpile(src, "escapes.gala")
	require.Error(t, err)

	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, galaerr.CodeInvalidStringEscape, se.Code)
	assert.Contains(t, se.Error(), `invalid escape sequence "\d" in string literal`)
}

// TestValidStringEscapesAccepted covers the positive path: every escape Go
// accepts must still transpile, and its text must survive verbatim into the
// generated Go literal.
func TestValidStringEscapesAccepted(t *testing.T) {
	trans := newEscapeTranspiler()

	cases := []struct {
		name string
		// arg is the literal passed to Println, so it is always used.
		arg string
		// wantInGo is a fragment that must appear in the generated Go.
		wantInGo string
	}{
		{name: "simple escapes", arg: `"a\ab\bf\fn\nr\rt\tv\v"`, wantInGo: `\ab\bf\fn\nr\rt\tv\v`},
		{name: "escaped backslash", arg: `"C:\\path"`, wantInGo: `C:\\path`},
		{name: "escaped double quote", arg: `"say \"hi\""`, wantInGo: `say \"hi\"`},
		{name: "regular expression with doubled backslash", arg: `"(\\d{4})"`, wantInGo: `(\\d{4})`},
		{name: "hex escape", arg: `"\x41\xff\xFF"`, wantInGo: `\x41\xff\xFF`},
		{name: "unicode 4-digit escape", arg: `"\u00e9\uFFFD"`, wantInGo: `\u00e9\uFFFD`},
		{name: "unicode 8-digit escape", arg: `"\U0001F600"`, wantInGo: `\U0001F600`},
		{name: "octal escape", arg: `"\000\101\377"`, wantInGo: `\000\101\377`},
		// GALA's CHAR_LIT grammar admits exactly one character after the
		// backslash, so the numeric escape forms (\xHH, \uHHHH, \UHHHHHHHH,
		// \OOO) are not lexable in a rune literal at all - that is a grammar
		// limit, reported as a syntax error before this check runs.
		{name: "rune literal newline escape", arg: `'\n'`, wantInGo: `'\n'`},
		{name: "rune literal tab escape", arg: `'\t'`, wantInGo: `'\t'`},
		{name: "rune literal escaped quote", arg: `'\''`, wantInGo: `'\''`},
		{name: "rune literal escaped backslash", arg: `'\\'`, wantInGo: `'\\'`},
		{name: "raw backtick string keeps backslashes literal", arg: "`(\\d{4})`", wantInGo: "`(\\d{4})`"},
		{name: "raw backtick string with an otherwise-invalid escape", arg: "`a\\qb`", wantInGo: "`a\\qb`"},
		{name: "interpolated string with valid escapes", arg: `s"tab\there\n"`, wantInGo: `tab\there\n`},
		{name: "format string with valid escapes", arg: `f"tab\there\n"`, wantInGo: `tab\there\n`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc main() {\n    Println(" + tc.arg + ")\n}\n"

			out, err := trans.Transpile(src, "escapes.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.wantInGo)
			assert.NotContains(t, out, transpiler.LineMarkerPrefix,
				"source-map markers must be lowered to //line directives")
		})
	}
}

// TestInterpolationExpressionEscapesUnaffected pins that a `${...}` block is
// skipped by the outer literal's escape scan: what is inside belongs to the
// embedded GALA expression, which is validated on its own when re-parsed.
func TestInterpolationExpressionEscapesUnaffected(t *testing.T) {
	trans := newEscapeTranspiler()

	cases := []struct {
		name string
		body string
	}{
		{name: "nested string literal in an interpolation", body: `    val s = s"v=${ "inner" }"`},
		{name: "format string with a nested string literal", body: `    val s = f"v=${ "inner" }%s"`},
		{name: "identifier interpolation next to a valid escape", body: "    val n = 1\n    val s = s\"n=$n\\tdone\""},
		{name: "escape immediately before an interpolation", body: "    val n = 1\n    val s = s\"\\t${ n }\""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nfunc main() {\n" + tc.body + "\n    Println(s)\n}\n"
			_, err := trans.Transpile(src, "escapes.gala")
			require.NoError(t, err)
		})
	}
}

// TestInvalidEscapeInsideInterpolationExpression pins that a genuinely invalid
// escape written inside a `${...}` block is still rejected: skipping the block
// in the outer scan does not skip the defect, because the embedded expression's
// own literals go through the normal validation path when re-parsed.
func TestInvalidEscapeInsideInterpolationExpression(t *testing.T) {
	trans := newEscapeTranspiler()

	src := "package main\n\nfunc main() {\n    val s = s\"v=${ \"in\\qner\" }\"\n    Println(s)\n}\n"
	_, err := trans.Transpile(src, "escapes.gala")
	require.Error(t, err)

	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, galaerr.CodeInvalidStringEscape, se.Code)
	assert.Contains(t, se.Error(), `invalid escape sequence "\q"`)
}

// TestStructFieldTagEscapes covers the second site where a GALA string token is
// copied verbatim into generated Go: a struct field tag.
func TestStructFieldTagEscapes(t *testing.T) {
	trans := newEscapeTranspiler()

	const valid = "package main\n\ntype User struct {\n" +
		"    Name string \"json:\\\"name\\\"\"\n" +
		"}\n\nfunc main() {\n    Println(\"ok\")\n}\n"

	out, err := trans.Transpile(valid, "escapes.gala")
	require.NoError(t, err)
	assert.Contains(t, out, `json:\"name\"`)

	const invalid = "package main\n\ntype User struct {\n" +
		"    Name string \"json:\\\"name\\\" bad:\\d\"\n" +
		"}\n\nfunc main() {\n    Println(\"ok\")\n}\n"

	_, err = trans.Transpile(invalid, "escapes.gala")
	require.Error(t, err)

	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, galaerr.CodeInvalidStringEscape, se.Code)
	assert.Contains(t, se.Error(), "in struct field tag")
}

// TestInvalidEscapeDoesNotLeakMarkers is the end-to-end guard for the reported
// failure mode. Transpiling an invalid escape must fail; it must never return
// code containing raw `__gala_line_*` source-map markers, which are internal
// identifiers the user never wrote and the Go compiler rejects with an
// "undefined" error naming a symbol that appears nowhere in their source.
func TestInvalidEscapeDoesNotLeakMarkers(t *testing.T) {
	trans := newEscapeTranspiler()

	src := "package main\n\nfunc main() {\n    val pattern = \"(\\d{4})\"\n    Println(pattern)\n}\n"
	out, err := trans.Transpile(src, "escapes.gala")

	require.Error(t, err, "the transpiler must not report success for unusable output")
	assert.NotContains(t, out, transpiler.LineMarkerPrefix,
		"internal source-map markers must never reach the caller as Go code")
	assert.Empty(t, out, "a failed transpile returns no code")
}
