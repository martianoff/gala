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
// writing these pages — E0002, E0003, E0006, E0015, E0018, E0027 through E0031,
// E0035, E0036 and E0038 from the in-memory transpiler, plus E0025 and E0032
// from a temp directory (both need real files on disk: E0025 because the whole
// point of that code is that a sibling's imports do not propagate, E0032 because
// a collision needs two packages to collide). Every other code is deliberately
// absent rather than stubbed; a stub would advertise coverage that does not
// exist. See the GALA-E0010 and GALA-E0026 notes in the table for codes that
// cannot be guarded from here at all.
//
// Most rows assert the whole rendered block. A row may instead assert a single
// line when that is all its page quotes — see renderEscapeVariant — but never
// less than the page claims, since a row that cannot fail is worse than no row.
//
// Repros are held to the same standard as the pages. A repro is supposed to
// fail, so a fictional API inside one has nowhere to surface: an E0036 repro
// once called `io.OpenFile`, which does not exist (GALA's `io` is the IO monad),
// and passed anyway because E0036 fires during statement transformation, before
// symbol resolution. Remediation snippets get compile-verified; repros do not.
// So when adding a row, read its repro for whether every symbol and import is
// real, not merely whether it triggers the code.
func TestErrorDocsQuoteRealOutput(t *testing.T) {
	cases := []struct {
		// name distinguishes several rows for the same code. A page often
		// documents more than one shape of the same diagnostic, and those
		// extra shapes are the ones most likely to drift, so each gets its
		// own row rather than the table being keyed by code alone.
		name string
		code galaerr.ErrorCode
		// render produces the exact diagnostic block the doc must quote.
		render func(t *testing.T) string
	}{
		{
			name: "sealed match missing a variant",
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
			name: "non-sealed match with no default",
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
			name: "two default arms",
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
			name: "bare return in a value-producing match",
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
			// Local sealed type: nothing to qualify, so the hint prints both
			// names bare.
			name: "unqualified constructor, same package",
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
			// The subtle half: the constructor comes from an ordinary import,
			// so a hint that printed bare names would suggest two identifiers
			// that are not in scope at the call site. This row pins that the
			// hint carries the qualifier the user actually wrote.
			name:   "qualified constructor behind a plain import",
			code:   galaerr.CodeSealedVariantUninferred, // GALA-E0018
			render: renderQualifiedVariantRepro,
		},
		{
			// Named package: the "used in ..." context is package-qualified.
			name:   "offender in a named package",
			code:   galaerr.CodeUnresolvedCrossPackageSymbol, // GALA-E0025
			render: renderUnresolvedSymbolRepro,
		},
		// The package-main shape of GALA-E0025 is documented on the page but
		// not pinned here. Its message differs by more than a package name —
		// the "used in ..." context is printed bare (`used in BuildLabels`)
		// rather than qualified — and it was captured from `gala build`. The
		// same source laid out for this in-process entry point compiles
		// without error, so the check is not reached from here and there is
		// nothing for a row to assert. Table rows are keyed by code + name
		// precisely so this row can be added once that gap is understood;
		// leaving a row that silently passes would be worse than its absence.

		// GALA-E0026 is deliberately absent, and cannot be added. Its
		// precondition — two dot-imported packages each declaring a sealed
		// case of the same name — is a strict subset of GALA-E0032's, and the
		// dot-import collision check runs first, so every repro that would
		// reach it reports E0032 instead. A row here could only ever assert
		// E0032's output, which is exactly the "row that cannot fail" this
		// table avoids. The page says the same thing in prose and quotes its
		// message from the emit site rather than from a run.
		{
			// Two declarations in one file. The cross-file shape the page also
			// documents is not pinned: it needs a sibling on disk, and the
			// batch entry point that reports both sites renders the terse
			// single-line form rather than the framed block this test asserts.
			name: "two functions of the same name in one file",
			code: galaerr.CodeFunctionRedeclared, // GALA-E0027
			render: func(t *testing.T) string {
				return renderRepro(t, "a.gala", `package main

func greet(name string) string = "hello " + name

func greet(other string) string = "hi " + other

func main() {
    Println(greet("world"))
}
`)
			},
		},
		{
			name: "two aliases of the same name",
			code: galaerr.CodeTypeAliasRedeclared, // GALA-E0028
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

type Handler func(string) string
type Handler func(int) int

func main() {
    Println("unused")
}
`)
			},
		},
		{
			name: "two method specs of the same name",
			code: galaerr.CodeInterfaceMethodRedeclared, // GALA-E0029
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

type Repo interface {
    Find(id int) string
    Find(name string) string
}

func main() {
    Println("unused")
}
`)
			},
		},
		{
			// Shorthand struct syntax.
			name: "duplicate field, shorthand struct",
			code: galaerr.CodeStructFieldRedeclared, // GALA-E0030
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

struct Point(X int, X int)

func main() {
    val p = Point(1, 2)
    Println(p.X)
}
`)
			},
		},
		{
			// Block struct syntax reaches a different emit site in the
			// analyzer, so it gets its own row.
			name: "duplicate field, block struct",
			code: galaerr.CodeStructFieldRedeclared, // GALA-E0030
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

type Point struct {
    val x int
    val x int
}

func main() {
    val p = Point(x = 1)
    Println(p.x)
}
`)
			},
		},
		{
			name: "two sealed cases of the same name",
			code: galaerr.CodeSealedVariantCaseRedeclared, // GALA-E0031
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

sealed type Shape {
    case Box(W int)
    case Box(H int)
}

func main() {
    val b = Box(1)
    Println(b)
}
`)
			},
		},
		{
			// Needs two real dot-imported packages on disk for the collision
			// to exist at all.
			name:   "same symbol from two dot-imported packages",
			code:   galaerr.CodeDotImportCollision, // GALA-E0032
			render: renderDotImportCollisionRepro,
		},
		{
			// The `len` row also pins the terse caret annotation, which is
			// derived by truncating the hint at its first " (" — a detail no
			// other covered code exercises.
			name: "bare len call",
			code: galaerr.CodeForbiddenGoBuiltin, // GALA-E0035
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

func main() {
    val s = "héllo"
    val n = len(s)
    Println(n)
}
`)
			},
		},
		{
			// Deliberately import-free. A bare keyword is rejected during
			// statement transformation, before any symbol is resolved, so the
			// repro needs nothing else to trigger it — and an import here
			// would be staged into the Linux sandbox or not, making the row
			// pass on Windows and fail on CI for reasons unrelated to E0036.
			name: "bare defer statement",
			code: galaerr.CodeForbiddenStatementKeyword, // GALA-E0036
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

func main() {
    defer
    Println("done")
}
`)
			},
		},
		{
			// The full framed block: locus, source row, caret row and hint.
			// This is the row that pins the caret annotation, including its
			// truncation to `\UH…` at terseHint's 60-rune cap — the exact
			// detail a page author is most likely to "repair" by hand after
			// assuming their terminal clipped it.
			//
			// Import-free, like the E0036 row above and for the same reason:
			// an escape is rejected while the literal is transformed, so
			// nothing else is needed to trigger it.
			name: "unrecognised escape in a regular expression",
			code: galaerr.CodeInvalidStringEscape, // GALA-E0038
			render: func(t *testing.T) string {
				return renderRepro(t, "esc.gala", `package main

func main() {
    val pattern = "(\d{4})"
    Println(pattern)
}
`)
			},
		},
		// The four malformed-escape variants below. Each is a real diagnostic
		// driven through the same renderer, but the row asserts only the
		// message line — see renderEscapeVariant for why that is the whole of
		// what those blocks claim.
		{
			name:   "hex escape with too few digits",
			code:   galaerr.CodeInvalidStringEscape, // GALA-E0038
			render: renderEscapeVariant(`val s = "\x4"`),
		},
		{
			name:   "octal escape above the maximum byte value",
			code:   galaerr.CodeInvalidStringEscape, // GALA-E0038
			render: renderEscapeVariant(`val s = "\400"`),
		},
		{
			name:   "unicode escape naming a surrogate half",
			code:   galaerr.CodeInvalidStringEscape, // GALA-E0038
			render: renderEscapeVariant(`val s = "\uD800"`),
		},
		{
			name:   "single quote escaped inside a string literal",
			code:   galaerr.CodeInvalidStringEscape, // GALA-E0038
			render: renderEscapeVariant(`val s = "it\'s"`),
		},
		{
			name: "Go slice type as an explicit type argument",
			code: galaerr.CodeGoTypeInExpression, // GALA-E0040
			render: func(t *testing.T) string {
				return renderRepro(t, "typearg.gala", `package main

import "martianoff/gala/collection_immutable"

func main() {
    val m = collection_immutable.EmptyHashMap[string, []byte]()
    Println(m.Size())
}
`)
			},
		},
		{
			name: "Go map type as an explicit type argument",
			code: galaerr.CodeGoTypeInExpression, // GALA-E0040
			render: func(t *testing.T) string {
				return renderRepro(t, "maparg.gala", `package main

import "martianoff/gala/collection_immutable"

func main() {
    val m = collection_immutable.EmptyHashMap[string, map[string]int]()
    Println(m.Size())
}
`)
			},
		},
		{
			// Import-free for the same reason as the E0036/E0038 rows: the
			// collision is decided from the matched type's own metadata while
			// the pattern is transformed, so a locally declared sealed type is
			// the whole repro.
			name: "bare variant name used as a match pattern",
			code: galaerr.CodeBareVariantBinding, // GALA-E0039
			render: func(t *testing.T) string {
				return renderRepro(t, "main.gala", `package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val s = Square(2.0)
    val r = s match {
        case Circle    => "circle"
        case Square(x) => s"square $x"
    }
    Println(r)
}
`)
			},
		},
		// The GALA-E0038 page also documents the rune-literal shape in prose
		// (`'\d'`), but quotes no output for it, so there is nothing to pin.
		// Its numeric forms (`'\x41'`) are not guardable here at all: GALA's
		// CHAR_LIT admits exactly one character after the backslash, so those
		// fail as ANTLR syntax errors before this check runs, and a row for
		// them would assert a message the escape validator never emits.
	}

	for _, tc := range cases {
		t.Run(string(tc.code)+"/"+tc.name, func(t *testing.T) {
			want := tc.render(t)
			require.NotEmpty(t, want, "renderer produced no diagnostic")
			require.Contains(t, want, string(tc.code),
				"repro did not fail with the code this page documents")

			doc := readErrorDoc(t, tc.code)
			require.Contains(t, doc, want,
				"docs/errors/%s.md does not quote the compiler's real output for %q.\n"+
					"Replace (or add) the page's `Error output` block with exactly:\n\n%s\n",
				tc.code, tc.name, want)
		})
	}
}

// renderEscapeVariant drives GALA-E0038 for one malformed escape and returns
// only the FIRST LINE of the rendered diagnostic.
//
// The GALA-E0038 page quotes these four variants as bare message lines rather
// than as framed blocks, and that is an editorial choice worth preserving: they
// exist to contrast the `reason` clause after the colon, and four near-identical
// frames would bury that contrast under ~28 lines of repeated scaffolding. So
// the row asserts exactly what those blocks claim — the message text — and
// nothing they do not show. The caret row, locus and hint are pinned by the full
// framed row above, which quotes an entire block.
//
// This still fails on drift: the reason clause is the most detailed text on the
// page and any rewording of it breaks these rows. What it deliberately does not
// do is assert a frame the page never printed.
func renderEscapeVariant(body string) func(t *testing.T) string {
	return func(t *testing.T) string {
		t.Helper()
		src := "package main\n\nfunc main() {\n    " + body + "\n    Println(s)\n}\n"
		full := renderRepro(t, "esc.gala", src)
		line, _, found := strings.Cut(full, "\n")
		require.True(t, found, "rendered diagnostic was a single line: %q", full)
		return line
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

// renderQualifiedVariantRepro drives GALA-E0018 at a call site that names the
// constructor through an ordinary import (`cmdpkg.NoCmd()`). The sealed type
// has to live in a real sibling package for the qualifier to exist at all, so
// this repro needs files on disk.
//
// This is the shape that catches a hint regressing to bare names: neither
// `Cmd` nor `NoCmd` is in scope in main.gala, so a bare suggestion here fails
// to compile with `undefined: NoCmd`.
func renderQualifiedVariantRepro(t *testing.T) string {
	t.Helper()
	// Import paths are resolved by stripping the module prefix and looking the
	// remainder up under a search root, so the sibling package is written as
	// <searchRoot>/cmdpkg and imported under the module's own prefix. The
	// directory name has to match the package name for the analyzer to
	// register it.
	searchRoot := t.TempDir()
	pkgDir := filepath.Join(searchRoot, "cmdpkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))

	cmdSrc := `package cmdpkg

sealed type Cmd[T any] {
    case NoCmd()
    case RunCmd(arg T)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "cmd.gala"), []byte(cmdSrc), 0o600))

	mainDir := filepath.Join(searchRoot, "main")
	require.NoError(t, os.MkdirAll(mainDir, 0o755))
	mainSrc := `package main

import "martianoff/gala/cmdpkg"

func main() {
    val x = cmdpkg.NoCmd()
    Println(x)
}
`
	mainPath := filepath.Join(mainDir, "main.gala")
	require.NoError(t, os.WriteFile(mainPath, []byte(mainSrc), 0o600))

	_, err := newDocGuardTranspilerWithPaths(searchRoot).Transpile(mainSrc, mainPath)
	require.Error(t, err, "repro was expected to fail to compile")

	rendered := galaerr.RenderRich(err, galaerr.Options{
		FallbackPath:   mainPath,
		FallbackSource: mainSrc,
		Color:          false,
	})
	rendered = strings.ReplaceAll(rendered, mainPath, "main.gala")
	rendered = strings.ReplaceAll(rendered, filepath.ToSlash(mainPath), "main.gala")
	return rendered
}

// renderDotImportCollisionRepro drives GALA-E0032. The collision is a property
// of two packages both exporting a name, so both have to exist on disk for the
// analyzer to discover their exports — an in-memory single file cannot express
// it. The reported package names come from the `package` clauses, not the
// import paths, so the module prefix used here does not leak into the message
// the page quotes.
func renderDotImportCollisionRepro(t *testing.T) string {
	t.Helper()
	searchRoot := t.TempDir()
	for _, pkg := range []string{"pkg_a", "pkg_b"} {
		dir := filepath.Join(searchRoot, pkg)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		src := "package " + pkg + "\n\nfunc Greet() string = \"hello from " + pkg + "\"\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, pkg+".gala"), []byte(src), 0o600))
	}

	mainDir := filepath.Join(searchRoot, "main")
	require.NoError(t, os.MkdirAll(mainDir, 0o755))
	mainSrc := `package main

import (
    . "martianoff/gala/pkg_a"
    . "martianoff/gala/pkg_b"
)

func main() {
    Println("hi")
}
`
	mainPath := filepath.Join(mainDir, "main.gala")
	require.NoError(t, os.WriteFile(mainPath, []byte(mainSrc), 0o600))

	_, err := newDocGuardTranspilerWithPaths(searchRoot).Transpile(mainSrc, mainPath)
	require.Error(t, err, "repro was expected to fail to compile")

	rendered := galaerr.RenderRich(err, galaerr.Options{
		FallbackPath:   mainPath,
		FallbackSource: mainSrc,
		Color:          false,
	})
	rendered = strings.ReplaceAll(rendered, mainPath, "main.gala")
	rendered = strings.ReplaceAll(rendered, filepath.ToSlash(mainPath), "main.gala")
	return rendered
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
	return newDocGuardTranspilerWithPaths()
}

// newDocGuardTranspilerWithPaths builds a transpiler whose analyzer searches
// the std sources plus any extra roots, so a repro can import a package it
// wrote into a temp directory.
func newDocGuardTranspilerWithPaths(extraRoots ...string) transpiler.Transpiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, append(getStdSearchPath(), extraRoots...))
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

	rel := filepath.Join("docs", "errors", string(code)+".md")
	data, err := os.ReadFile(filepath.Join(roots[0], rel))
	require.NoError(t, err, docPageUnreadableMsg, rel)
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// docPageUnreadableMsg names the usual cause of an unreadable page. Under
// Bazel the likeliest reason is not a missing file but a page that exists in
// the source tree and was never added to the //docs/errors:error_docs
// filegroup, so it was not staged into the sandbox. A bare "no such file"
// sends the reader hunting for something that is sitting right there.
const docPageUnreadableMsg = "could not read %s. If the page exists in the " +
	"source tree, it is probably missing from the //docs/errors:error_docs " +
	"filegroup, so it was not staged into the test sandbox."
