package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestErrorDocsQuoteRealOutput closes the loop between the compiler's
// diagnostics and the pages under docs/errors/.
//
// Those pages exist so a user can paste a message they just saw into a search
// engine and land on the explanation. That only works if the quoted text is
// byte-identical to what the compiler emits — and nothing previously enforced
// that, so the pages drifted: hints were dropped, caret rows were invented,
// and one page quoted a message no emit site produces.
//
// For each covered code this test compiles a minimal repro, renders the
// resulting diagnostic through the same CLI renderer a user sees
// (galaerr.RenderRich with color off), and asserts the doc page contains that
// exact block. A wording change in the transpiler therefore fails here until
// the page is updated with the new text.
//
// Scope note: the table covers the codes whose real output was captured while
// writing these pages — E0002, E0003, E0006, E0015, E0018 from the in-memory
// transpiler and E0025 from a temp directory (it needs a sibling file on disk,
// because the whole point of that code is that a sibling's imports do not
// propagate). Every other code is deliberately absent rather than stubbed; a
// stub would advertise coverage that does not exist. See the GALA-E0010 note
// in the table for a code that cannot be guarded from here at all.
func TestErrorDocsQuoteRealOutput(t *testing.T) {
	cases := []struct {
		code galaerr.ErrorCode
		// render produces the exact diagnostic block the doc must quote.
		render func(t *testing.T) string
	}{
		{
			code: galaerr.CodeNonExhaustiveMatch, // GALA-E0002
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
}

func main() {
    Println(name(Red()))
}
`)
			},
		},
		{
			code: galaerr.CodeMissingDefault, // GALA-E0003
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
}

func main() {
    Println(name(1))
}
`)
			},
		},
		{
			code: galaerr.CodeMultipleDefaults, // GALA-E0006
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

func name(n int) string = n match {
    case 1 => "one"
    case _ => "other"
    case _ => "also-other"
}

func main() {
    Println(name(1))
}
`)
			},
		},
		// GALA-E0010 is deliberately absent. It has two emit sites in the
		// analyzer that produce different text ("package file X declares
		// package ..." during sibling discovery, "directory D has files with
		// different package names" during sibling filtering), and only the
		// first is on the path the CLI takes. The site reachable from this
		// in-memory entry point is the second one, and its message embeds an
		// absolute directory path, so there is no stable text for a page to
		// quote. Guarding it here would lock in a message users never see.
		{
			code: galaerr.CodeBareReturnInValueMatch, // GALA-E0015
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

import "os"

func run(path string) {
    val data = Try(os.ReadFile(path)) match {
        case Success(b)   => string(b)
        case Failure(err) => {
            Println(s"error: ${err.Error()}")
            return
        }
    }
    Println(data)
}

func main() {
    run("missing.txt")
}
`)
			},
		},
		{
			code: galaerr.CodeSealedVariantUninferred, // GALA-E0018
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

sealed type Box[T any] {
    case Empty()
    case Filled(value T)
}

func main() {
    val x = Empty()
    Println(x)
}
`)
			},
		},
		{
			code:   galaerr.CodeUnresolvedCrossPackageSymbol, // GALA-E0025
			render: renderUnresolvedSymbolRepro,
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			want := tc.render(t)
			require.NotEmpty(t, want, "renderer produced no diagnostic")
			require.Contains(t, want, string(tc.code),
				"repro did not fail with the code this page documents")

			doc := readErrorDoc(t, tc.code)
			require.Contains(t, doc, want,
				"docs/errors/%s.md does not quote the compiler's real output.\n"+
					"Replace the page's `Error output` block with exactly:\n\n%s\n",
				tc.code, want)
		})
	}
}

// renderRepro transpiles src and renders the resulting error the way the CLI
// does: no color, with src supplied as the fallback source so the framed
// snippet is built from the in-memory repro rather than a file on disk.
func renderRepro(t *testing.T, path, src string) string {
	t.Helper()
	_, err := newDocGuardTranspiler().Transpile(src, path)
	require.Error(t, err, "repro was expected to fail to compile")
	return galaerr.RenderRich(err, galaerr.Options{
		FallbackPath:   path,
		FallbackSource: src,
		Color:          false,
	})
}

// renderUnresolvedSymbolRepro drives GALA-E0025. The offending file has to sit
// next to a sibling that *does* import the package, because the point of the
// code is that a sibling's imports do not propagate — so the repro needs real
// files on disk for the analyzer's sibling discovery to find.
func renderUnresolvedSymbolRepro(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "effects")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	seedSrc := `package effects

import . "martianoff/gala/collection_immutable"

func Seed() Array[string] = ArrayOf("a", "b")
`
	labelsSrc := `package effects

func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seed.gala"), []byte(seedSrc), 0o600))
	labelsPath := filepath.Join(dir, "labels.gala")
	require.NoError(t, os.WriteFile(labelsPath, []byte(labelsSrc), 0o600))

	_, err := newDocGuardTranspiler().Transpile(labelsSrc, labelsPath)
	require.Error(t, err, "repro was expected to fail to compile")

	rendered := galaerr.RenderRich(err, galaerr.Options{
		FallbackPath:   labelsPath,
		FallbackSource: labelsSrc,
		Color:          false,
	})
	// The temp directory is per-run, so the absolute path in the locus is not
	// stable text a doc page could quote. Normalize it to the workspace-
	// relative name the page shows.
	rendered = strings.ReplaceAll(rendered, labelsPath, "effects/labels.gala")
	rendered = strings.ReplaceAll(rendered, filepath.ToSlash(labelsPath), "effects/labels.gala")
	return rendered
}

func newDocGuardTranspiler() transpiler.Transpiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// readErrorDoc returns the text of docs/errors/<code>.md. getStdSearchPath
// already resolves the workspace root for this package's tests — through Bazel
// runfiles when the test runs under Bazel, by walking up to go.mod otherwise —
// and the pages sit under that same root in both layouts, so it doubles as the
// doc root and no second lookup strategy is needed.
//
// Newlines are normalized so the comparison does not depend on how git checked
// the page out: the renderer always emits "\n", and a CRLF working copy on
// Windows would otherwise fail every case for a reason that has nothing to do
// with message drift.
func readErrorDoc(t *testing.T, code galaerr.ErrorCode) string {
	t.Helper()
	roots := getStdSearchPath()
	require.NotEmpty(t, roots, "could not locate the workspace root")

	data, err := os.ReadFile(filepath.Join(roots[0], "docs", "errors", string(code)+".md"))
	require.NoError(t, err, "error doc page not found")
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}
