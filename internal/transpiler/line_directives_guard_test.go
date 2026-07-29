package transpiler

import (
	"os"
	"path/filepath"
	"runtime"
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

			// The Go that failed to parse is the one artifact needed to
			// diagnose the bug, and this is the only place it exists — the
			// output file is never written on this path. The diagnostic must
			// name a dump of it rather than discard it.
			dump := dumpPathFromMessage(t, se.Error())
			t.Cleanup(func() { _ = os.Remove(dump) })
			written, readErr := os.ReadFile(dump)
			require.NoError(t, readErr, "the named dump must exist and be readable")
			assert.Equal(t, tc.code, string(written),
				"the dump must be the exact source that failed to parse")
		})
	}
}

// dumpPathFromMessage extracts the dump path the unparseable-Go diagnostic names.
func dumpPathFromMessage(t *testing.T, msg string) string {
	t.Helper()
	const prefix = "the generated Go was written to "
	idx := strings.Index(msg, prefix)
	require.NotEqual(t, -1, idx, "diagnostic must name the dump it wrote: %s", msg)
	rest := msg[idx+len(prefix):]
	end := strings.Index(rest, " for inspection)")
	require.NotEqual(t, -1, end, "diagnostic must close the dump clause: %s", msg)
	return rest[:end]
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

// TestInsertLineDirectivesPathSeparators pins that the emitted path uses forward
// slashes, which is the form the Go compiler's `//line` parser handles.
//
// The conversion is filepath.ToSlash, and it is deliberately PLATFORM-DEPENDENT:
// it rewrites the host's separator only. On Windows that turns C:\src\demo.gala
// into C:/src/demo.gala; on POSIX it is a documented no-op, because `\` is not a
// separator there but a legal character in a filename. Rewriting it
// unconditionally (strings.ReplaceAll) would corrupt the path of a POSIX file
// genuinely named `odd\name.gala` — so if the drive-letter case below ever fails,
// fix the test, not the conversion.
//
// The first half runs everywhere and still exercises the conversion on Windows:
// filepath.Join builds a host-native separator, which ToSlash must normalize to
// `/` on both platforms.
func TestInsertLineDirectivesPathSeparators(t *testing.T) {
	code := "package main\n\nfunc main() {\n\t" + LineMarkerName(4) + "\n\tprintln(\"hi\")\n}\n"

	// Cross-platform: a host-native relative path is emitted slash-separated.
	out, err := insertLineDirectives(code, filepath.Join("src", "demo.gala"))
	require.NoError(t, err)
	assert.Contains(t, out, "//line src/demo.gala:4")
	assert.NotContains(t, out, `src\demo.gala`)

	// Windows-only: a drive path loses its backslashes. On POSIX this input is
	// not a path at all — it is a single filename containing backslashes, which
	// ToSlash correctly leaves alone — so the expectation simply does not apply.
	if runtime.GOOS != "windows" {
		t.Skip("drive-path normalization is Windows-only: filepath.ToSlash is a no-op on POSIX, where `\\` is a legal filename character rather than a separator")
	}

	out, err = insertLineDirectives(code, `C:\src\demo.gala`)
	require.NoError(t, err)
	assert.Contains(t, out, "//line C:/src/demo.gala:4")
	assert.NotContains(t, out, `C:\src`,
		"backslashes confuse the compiler's //line parser")
}
