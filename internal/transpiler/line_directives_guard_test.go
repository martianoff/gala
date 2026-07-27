package transpiler

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertLineDirectives is the last pass before generated Go reaches a caller,
// and it is the pass that removes the transformer's internal `__gala_line_N`
// source-map markers. It used to treat "this buffer does not parse" as a
// tolerable condition and return its input unchanged — which meant the markers
// survived into the emitted file as bare identifiers and package-level vars.
// The Go compiler then failed with `undefined: __gala_line_4`, naming a symbol
// that appears nowhere in the user's GALA source, and the transpiler had
// already exited 0.
//
// The rewrite now refuses to degrade: it returns an error whenever it cannot
// account for every marker, so a codegen defect can never again ship
// marker-laden Go with a success status. These tests pin that contract
// independently of the specific defect (an invalid string escape) that exposed
// it, so any future one is caught too.

// TestInsertLineDirectivesRejectsUnparseableCode pins that unparseable input is
// reported rather than passed through with its markers intact.
func TestInsertLineDirectivesRejectsUnparseableCode(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{
			name: "invalid escape in a string literal",
			// Exactly the shape the generator emitted for `val p = "(\d{4})"`:
			// syntactically fine except that `\d` is not a Go escape, so the
			// buffer does not re-parse.
			code: "package main\n\nvar " + LineMarkerName(3) + " int\n\nfunc main() {\n\t" +
				LineMarkerName(4) + "\n\tvar pattern = \"(\\d{4})\"\n\t_ = pattern\n}\n",
		},
		{
			name: "unbalanced braces",
			code: "package main\n\nfunc main() {\n\t" + LineMarkerName(4) + "\n",
		},
		{
			name: "not Go at all",
			code: "this is not go source\n" + LineMarkerName(1) + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := insertLineDirectives(tc.code, "demo.gala")

			require.Error(t, err, "unparseable generated Go must be reported, not absorbed")
			assert.Empty(t, out, "no code is returned alongside the error")

			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, galaerr.CodeInternalTransformerPanic, se.Code,
				"this is an internal transpiler failure, not a user error")

			// The message must be honest about what failed and name the file.
			assert.Contains(t, se.Error(), "internal transpiler error")
			assert.Contains(t, se.Error(), "demo.gala")
			assert.Contains(t, se.Error(), LineMarkerPrefix)
			assert.Contains(t, se.Hint, "transpiler bug")
		})
	}
}

// TestInternalErrorRendersWithoutBogusFrame pins that the 0,0 position these
// internal errors carry renders sanely. An internal-error path that itself
// rendered malformed would be an unfortunate way to discover a transpiler bug:
// the renderer must not emit a `--> file:0:0` locus, and must not try to read
// line 0 of the source (there is no such line — source lines are 1-based).
func TestInternalErrorRendersWithoutBogusFrame(t *testing.T) {
	code := "package main\n\nfunc main() {\n\tx := 1 + " + LineMarkerName(7) + "\n\t_ = x\n}\n"
	_, err := insertLineDirectives(code, "demo.gala")
	require.Error(t, err)

	// Render with source available, which is the case that would expose a
	// bogus frame or an out-of-range line read.
	out := galaerr.RenderRich(err, galaerr.Options{
		FallbackSource: "package main\n\nfunc main() {\n}\n",
		FallbackPath:   "demo.gala",
	})

	assert.NotContains(t, out, ":0:0", "must not emit a 0:0 locus")
	assert.NotContains(t, out, "-->", "a positionless error gets no source frame")
	assert.NotContains(t, out, "^", "no caret row without a frame")

	// The useful parts still render: the code, the message and the hint.
	assert.Contains(t, out, "GALA-E0017")
	assert.Contains(t, out, "internal transpiler error")
	assert.Contains(t, out, "transpiler bug")
}

// TestInsertLineDirectivesRejectsUnhandledMarkerPosition pins the second half of
// the guard: code that parses fine but puts a marker somewhere the rewrite does
// not handle. Such a marker would be emitted verbatim as Go, so it must be
// reported rather than silently left in place.
func TestInsertLineDirectivesRejectsUnhandledMarkerPosition(t *testing.T) {
	// The marker appears as an operand rather than as a bare statement or a
	// top-level var declaration — a shape the rewrite deliberately does not
	// claim, because rewriting it would corrupt the expression.
	code := "package main\n\nfunc main() {\n\tx := 1 + " + LineMarkerName(7) + "\n\t_ = x\n}\n"

	out, err := insertLineDirectives(code, "demo.gala")

	require.Error(t, err)
	assert.Empty(t, out)

	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, galaerr.CodeInternalTransformerPanic, se.Code)
	assert.Contains(t, se.Error(), "the //line rewrite does not handle")
}

// TestInsertLineDirectivesHappyPath pins that valid marker-bearing code still
// lowers to `//line` directives and leaves no marker behind — the guard must
// not fire on correct input.
func TestInsertLineDirectivesHappyPath(t *testing.T) {
	code := "package main\n\nvar " + LineMarkerName(3) + " int\n\nfunc main() {\n\t" +
		LineMarkerName(6) + "\n\tprintln(\"hi\")\n}\n"

	out, err := insertLineDirectives(code, "demo.gala")

	require.NoError(t, err)
	assert.NotContains(t, out, LineMarkerPrefix,
		"every marker must be lowered to a //line directive")
	assert.Contains(t, out, "//line demo.gala:3")
	assert.Contains(t, out, "//line demo.gala:6")
	assert.Contains(t, out, `println("hi")`)
}

// TestInsertLineDirectivesNoMarkers pins the no-op case: code with no markers is
// returned unchanged and without error.
func TestInsertLineDirectivesNoMarkers(t *testing.T) {
	code := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"

	out, err := insertLineDirectives(code, "demo.gala")

	require.NoError(t, err)
	assert.Equal(t, code, out)
}

// TestInsertLineDirectivesWindowsPath pins that a Windows drive path is emitted
// with forward slashes, which is the form the Go compiler's `//line` parser
// handles.
func TestInsertLineDirectivesWindowsPath(t *testing.T) {
	code := "package main\n\nfunc main() {\n\t" + LineMarkerName(4) + "\n\tprintln(\"hi\")\n}\n"

	out, err := insertLineDirectives(code, `C:\src\demo.gala`)

	require.NoError(t, err)
	assert.Contains(t, out, "//line C:/src/demo.gala:4")
	assert.False(t, strings.Contains(out, `C:\src`),
		"backslashes confuse the compiler's //line parser")
}
