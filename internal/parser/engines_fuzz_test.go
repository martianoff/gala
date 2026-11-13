package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// FuzzParserEngines holds galop's lexer and parser to ANTLR's on arbitrary
// input, as the corpus tests (TestNativeLexMatchesANTLR, TestLexNativeAt,
// TestEnginesAgree) hold them on the repository's sources and a few broken
// variants of each, through the same compareLexers and compareEngines:
//
//   - galop never panics: its lexer and parser recover a panic and hand the
//     input to ANTLR's, which hides it from every other check, but each one
//     is a galop bug (noteGalopPanic);
//   - galop's native lexer gives ANTLR's tokens, hidden ones included, its
//     lexical errors and its doc comments, a BOM and invalid UTF-8 included;
//     at the start of the input and at an offset, as an expression's default
//     or interpolation is lexed;
//   - VisitNativeTokens visits the default-channel tokens VisitTokens visits;
//   - the two engines agree on the parse of a source file (compareEngines:
//     the same errors and doc comments, the same native tree, clean or
//     error-recovered, and a native tree whose helpers read as the ANTLR
//     tree's), and of an expression, alone and at an offset.
//
// tools/fuzz.sh runs it like the transpiler's targets.
func FuzzParserEngines(f *testing.F) {
	for _, s := range engineSeeds(f) {
		f.Add(s, 0, 0)
	}
	for k, s := range expressionAtInputs {
		f.Add(s, 1+k*37, k*5)
	}
	f.Fuzz(func(t *testing.T, src string, line, col int) {
		// An offset as the transpiler re-lexes a default or an
		// interpolation at: a line of the file, or none (line <= 0).
		line, col = line%1000, col%200
		if col < 0 {
			col = -col
		}
		before := galopPanics.Load()
		checkEnginesAgree(t, src, line, col)
		if n := galopPanics.Load() - before; n > 0 {
			t.Errorf("galop's lexer or parser panicked %d times (rerun with GALA_PANIC_STACK=1 for the stacks)", n)
		}
		if t.Failed() {
			t.Logf("input: %q at %d:%d", src, line, col)
		}
	})
}

// checkEnginesAgree reports every way galop's lexing and parsing of src
// differs from ANTLR's (see FuzzParserEngines).
func checkEnginesAgree(t *testing.T, src string, line, col int) {
	t.Helper()
	// The lexers from the start of the input: tokens, doc comments, the
	// token stream and the token visits.
	_, diffs := compareLexers(t, src)
	for _, d := range diffs {
		t.Error(d)
	}
	// The lexers at an offset. With line <= 0 both start at 1:0, whatever
	// col (lexNative, seedLexer), as compareLexers lexed. Both read the text
	// as an expression's is read, a leading BOM included.
	if line > 0 {
		if d := firstTokenDiff(antlrTokens(t, src, line, col), nativeTokens(src, line, col)); d != "" {
			t.Errorf("the lexers disagree at %d:%d: %s", line, col, d)
		}
	}
	// The parsers: a source file, an expression, an expression at an offset.
	// The last is parsed at 0:0 too: it is the checked parse (exactly one
	// expression, errors naming what is parsed), not ParseExpressionTree's.
	_, _, diffs = compareEngines(src, true)
	for _, d := range diffs {
		t.Errorf("source file: %s", d)
	}
	if w, g := parseExpressionWith(EngineANTLR, src), parseExpressionWith(EngineGalop, src); w != g {
		t.Errorf("expression: the engines disagree: %s", diffAt(w, g))
	}
	if w, g := parseExpressionAtWith(EngineANTLR, src, line, col), parseExpressionAtWith(EngineGalop, src, line, col); w != g {
		t.Errorf("expression at %d:%d: the engines disagree: %s", line, col, diffAt(w, g))
	}
}

// maxEngineSeedFiles caps the source files FuzzParserEngines seeds from, so
// its seed pass under `bazel test` takes seconds: TestEnginesAgree already
// compares the whole corpus. The sample is spread evenly over the files.
const maxEngineSeedFiles = 25

// engineSeeds is the position inputs, an even sample of the staged sources
// with their corpus variants (fileVariants: broken variants, a CRLF copy),
// and inputs around what galop has got wrong before.
func engineSeeds(tb testing.TB) []string {
	tb.Helper()
	files := sourceTexts(tb)
	step := max(1, (len(files)+maxEngineSeedFiles-1)/maxEngineSeedFiles)
	seeds := slices.Clone(positionInputs)
	for i := 0; i < len(files); i += step {
		seeds = append(seeds, fileVariants(files[i])...)
	}
	seeds = append(seeds, brokenInputs...)
	seeds = append(seeds, emptyLineInputs...)
	return append(seeds,
		// Invalid UTF-8, which ANTLR reads as U+FFFD, in a doc comment, an
		// identifier, cut at the end, after a BOM, and in a string literal,
		// where a byte dropped from a multi-byte character also breaks the
		// file (see antlrText).
		"package main\n\n// Two placeho\x94 sum via FoldLeft.\nfunc f() int = 1\n",
		"package main\n\n// A dash \xe2\x80 cut.\nval s = \"a\x94b\"\nval = \n",
		"package main\n\nval a\x94b = 1\nval ✓\x94 = 2\n",
		"package main\n\n// cut at the end \xe2\x82",
		"\xef\xbb\xbfpackage main\n\n// a \x94\nval x = 1 // (\n",
		// A conjured token in ANTLR's recovered tree: a missing or misspelt
		// `package`.
		"main\n\nval x = 1\n",
		"packe main\n\nval x = 1\n",
		"packag\n\nval x = 1\n",
		// A syntax error after clean lexing, in an interpolation; an
		// illegal character, a lexical error.
		"package main\n\nfunc main() {\n    Println(s\"v=${1 +}\")\n}\n",
		"package main\n\nval s = \"a\uFEFFb\"\n",
		"package main\n\nval x = 'ab\n",
		"package main\n\nval x = \"unterminated\n",
		"package main\n\n/* unterminated",
		// Line breaks the lexer re-types.
		"package main\n\nval x = f\n(1)\nval y = a\n*b\nval z = c\n&d\n",
		"package main\r\n\r\nval s = \"ä\"\r\n",
	)
}

// repoSources is every .gala file of the repository the test runs in, for
// gatherSources under plain `go test`: the files under the module root, but
// for hidden directories (the IDE's, agents' worktrees, vendored caches) and
// Bazel's output links.
func repoSources(tb testing.TB) []string {
	tb.Helper()
	root, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			tb.Fatal("cannot find the module root (go.mod)")
		}
		root = parent
	}
	var paths []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() && path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "bazel-") || name == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(name, ".gala") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return paths
}
