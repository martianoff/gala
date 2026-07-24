package galaerr_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// caretLineOf returns the frame line containing the caret run (the first line
// with a '^'), or "" if none. Used to assert caret column and width.
func caretLineOf(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "^") {
			return ln
		}
	}
	return ""
}

// afterPipe returns the substring following the first "| " on a frame line,
// i.e. the content aligned with the source text (or caret indent).
func afterPipe(line string) string {
	if i := strings.Index(line, "| "); i >= 0 {
		return line[i+2:]
	}
	return ""
}

const multiLineSrc = `package main

func main() {
    val n = len(s)
}
`

func TestRenderRich(t *testing.T) {
	t.Run("coded semantic error with hint and multi-line source", func(t *testing.T) {
		// `len` starts at 0-based rune column 12 on line 4; exact span 12..15.
		err := galaerr.NewCodedSemanticError(
			galaerr.CodeForbiddenGoBuiltin, 4, 12,
			"bare `len` is forbidden",
			"GALA strings & collections expose .Size()",
		).WithSpan(15)

		out := galaerr.RenderRich(err, galaerr.Options{FallbackPath: "foo.gala", FallbackSource: multiLineSrc})

		assert.Contains(t, out, "error[GALA-E0035]: bare `len` is forbidden")
		assert.Contains(t, out, "  --> foo.gala:4:13") // 0-based col 12 shown as 13
		assert.Contains(t, out, "4 |     val n = len(s)")
		assert.Contains(t, out, "= hint: GALA strings & collections expose .Size()")

		caret := caretLineOf(out)
		require.NotEmpty(t, caret, "expected a caret line")
		// 12 spaces of indent then a 3-wide caret under `len`.
		assert.Equal(t, strings.Repeat(" ", 12)+"^^^ GALA strings & collections expose .Size()", afterPipe(caret))
	})

	t.Run("exact-span caret width equals the token", func(t *testing.T) {
		// Point at `n` (a 1-char name) but set an exact span covering `len` (3).
		err := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 4, 12, "m", "h").WithSpan(15)
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: multiLineSrc})
		assert.Contains(t, afterPipe(caretLineOf(out)), "^^^")
		assert.NotContains(t, afterPipe(caretLineOf(out)), "^^^^") // exactly 3
	})

	t.Run("derived-span caret width scans the identifier", func(t *testing.T) {
		// No exact span: width is derived by scanning the identifier run `len`.
		err := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 4, 12, "m", "h")
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: multiLineSrc})
		assert.Equal(t, strings.Repeat(" ", 12)+"^^^ h", afterPipe(caretLineOf(out)))
	})

	t.Run("derived string span reaches closing quote", func(t *testing.T) {
		src := `    val s = "hello"` + "\n"
		// `"` starts at rune column 12; span should cover `"hello"` (7 chars).
		err := galaerr.NewSemanticErrorAt(1, 12, "unexpected string")
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: src})
		assert.Contains(t, afterPipe(caretLineOf(out)), "^^^^^^^") // 7 carets
	})

	t.Run("no position renders header and hint only", func(t *testing.T) {
		err := galaerr.NewCodedSemanticError(galaerr.CodeUndefinedVariable, 0, 0, "undefined variable x", "check the spelling")
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: multiLineSrc})
		assert.Contains(t, out, "error[GALA-E0023]: undefined variable x")
		assert.Contains(t, out, "= hint: check the spelling")
		assert.NotContains(t, out, "-->")
		assert.NotContains(t, out, "^")
		assert.NotContains(t, out, "|")
	})

	t.Run("uncoded error omits the bracketed code", func(t *testing.T) {
		err := galaerr.NewSemanticErrorAt(0, 0, "something went wrong")
		out := galaerr.RenderRich(err, galaerr.Options{})
		assert.True(t, strings.HasPrefix(out, "error: something went wrong"),
			"want plain `error:` header, got %q", out)
		assert.NotContains(t, out, "error[")
	})

	t.Run("multi-error renders each diagnostic", func(t *testing.T) {
		e1 := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 4, 12, "first", "h1").WithSpan(15)
		e2 := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 4, 12, "second", "h2").WithSpan(15)
		multi := &galaerr.MultiError{Errors: []error{e1, e2}}
		out := galaerr.RenderRich(multi, galaerr.Options{FallbackSource: multiLineSrc})
		assert.Contains(t, out, "error[GALA-E0035]: first")
		assert.Contains(t, out, "error[GALA-E0035]: second")
		assert.Equal(t, 2, strings.Count(out, "^^^"), "expected two caret frames")
		assert.Contains(t, out, "\n\n", "expected a blank line between diagnostics")
	})

	t.Run("tab-indented source keeps the caret aligned", func(t *testing.T) {
		src := "\tval n = len(s)\n" // `len` at rune column 9 (tab + "val n = ")
		err := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 1, 9, "bare len", "h").WithSpan(12)
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: src})
		// The caret indent replicates the leading tab, then 8 spaces, then ^^^.
		assert.Equal(t, "\t"+strings.Repeat(" ", 8)+"^^^ h", afterPipe(caretLineOf(out)))
		// The snippet keeps the literal tab.
		assert.Contains(t, out, "1 | \tval n = len(s)")
	})

	t.Run("multi-byte column keeps the caret aligned", func(t *testing.T) {
		// `é` is one rune but two bytes; rune-indexed column must not misalign.
		src := "val café = len(x)\n" // `len` at rune column 11
		err := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 1, 11, "bare len", "h").WithSpan(14)
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: src})
		assert.Equal(t, strings.Repeat(" ", 11)+"^^^ h", afterPipe(caretLineOf(out)))
	})

	t.Run("color off emits zero ANSI escapes", func(t *testing.T) {
		err := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 4, 12, "bare len", "h").WithSpan(15)
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: multiLineSrc, Color: false})
		assert.NotContains(t, out, "\x1b[")
	})

	t.Run("color on includes ANSI escapes", func(t *testing.T) {
		err := galaerr.NewCodedSemanticError(galaerr.CodeForbiddenGoBuiltin, 4, 12, "bare len", "h").WithSpan(15)
		out := galaerr.RenderRich(err, galaerr.Options{FallbackSource: multiLineSrc, Color: true})
		assert.Contains(t, out, "\x1b[1;31m") // red+bold header/caret
		assert.Contains(t, out, "\x1b[0m")    // reset
	})

	t.Run("non-GALA error passes through verbatim", func(t *testing.T) {
		err := assertError("go build: ./main.gen.go:9:2: undefined: Foo")
		out := galaerr.RenderRich(err, galaerr.Options{})
		assert.Equal(t, err.Error(), out)
	})
}

// assertError is a tiny error wrapper for the passthrough test.
type assertError string

func (e assertError) Error() string { return string(e) }
