package parser

import (
	"slices"
	"testing"

	"martianoff/gala/galaerr"
)

// FuzzParser parses arbitrary input as a source file and as an expression,
// alone and at an offset, as the transpiler re-parses a default value or an
// interpolation:
//
//   - galop's lexer and parser never panic: a panic is recovered and
//     reported as an internal compiler error (IsInternalError), which hides
//     nothing from a build, but each one is a galop bug;
//   - every parse of a source file has a tree, clean when it reports no
//     error and error-recovered when it does;
//   - VisitNativeTokens visits the default-channel tokens the lexer gives;
//   - lexed at an offset, the first token starts there.
//
// galop's repository holds its lexer and parser to ANTLR's on arbitrary
// input; the parser golden (TestParserGolden) holds what GALA makes of them.
// tools/fuzz.sh runs it like the transpiler's targets.
func FuzzParser(f *testing.F) {
	for _, s := range fuzzSeeds(f) {
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
		checkParse(t, src, line, col)
		if t.Failed() {
			t.Logf("input: %q at %d:%d", src, line, col)
		}
	})
}

// checkParse reports every way the parses of src break FuzzParser's
// properties.
func checkParse(t *testing.T, src string, line, col int) {
	t.Helper()
	p := NewAntlrGalaParser().ParseFile(src)
	for _, err := range p.Errors {
		if IsInternalError(err) {
			t.Errorf("source file: %v (rerun with GALA_PANIC_STACK=1 for the stack)", err)
			return
		}
	}
	if _, err := p.RecoveredNative().Tree(); err != nil {
		t.Errorf("source file: no tree (errors %v)", p.Errors)
	}
	if (p.CleanNative() != nil) != (len(p.Errors) == 0) {
		t.Errorf("source file: a clean tree %v with errors %v", p.CleanNative() != nil, p.Errors)
	}

	ts := lexNativeRecovered(galaerr.StripBOM(src))
	if ts == nil {
		t.Error("galop's lexer panicked")
		return
	}
	var want, visited []string
	for _, tok := range ts.Tokens[:len(ts.Tokens)-1] { // without EOF
		want = append(want, tok.Text(ts.Src))
	}
	VisitNativeTokens(src, func(text string) { visited = append(visited, text) })
	if !slices.Equal(want, visited) {
		t.Errorf("VisitNativeTokens visits %q, the lexer gives %q", visited, want)
	}

	if _, errs := ParseExpressionTree(src); len(errs) > 0 && IsInternalError(errs[0]) {
		t.Errorf("expression: %v", errs[0])
	}
	if _, err := ParseExpressionCheckedAt(src, line, col, "test expression"); IsInternalError(err) {
		t.Errorf("expression at %d:%d: %v", line, col, err)
	}
	if line > 0 {
		if tok := lexNative(src, line, col).Tokens[0]; tok.Line < int32(line) || tok.Line == int32(line) && tok.Column < int32(col) {
			t.Errorf("lexed at %d:%d, the first token starts at %d:%d", line, col, tok.Line, tok.Column)
		}
	}
}

// maxSeedFiles caps the source files FuzzParser seeds from, so its seed pass
// under `bazel test` takes seconds. The sample is spread evenly over the
// files.
const maxSeedFiles = 25

// fuzzSeeds is the position inputs, an even sample of the staged sources
// with their variants (fileVariants: broken variants, a CRLF copy), and the
// fixed inputs of the parser golden.
func fuzzSeeds(tb testing.TB) []string {
	tb.Helper()
	files := sourceTexts(tb)
	step := max(1, (len(files)+maxSeedFiles-1)/maxSeedFiles)
	seeds := slices.Clone(positionInputs)
	for i := 0; i < len(files); i += step {
		seeds = append(seeds, fileVariants(files[i])...)
	}
	return append(seeds, goldenFixedLexInputs()...)
}
