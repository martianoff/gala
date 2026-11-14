package parser

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// printNative prints ts: each token, hidden ones in their place, then each
// lexical error.
func printNative(ts *native.GalaTokens) []string {
	var out []string
	for i, tok := range ts.Tokens {
		for _, h := range ts.HiddenBefore(i) {
			out = append(out, printToken(int(h.Type), int(h.Channel), int(h.Line), int(h.Column), h.Text(ts.Src)))
		}
		out = append(out, printToken(int(tok.Type), native.GalaDefaultChannel, int(tok.Line), int(tok.Column), tok.Text(ts.Src)))
	}
	for _, e := range ts.Errors {
		out = append(out, fmt.Sprintf("%d:%d %s", e.Line, e.Column, e.Msg))
	}
	return out
}

func printToken(ttype, channel, line, col int, text string) string {
	return fmt.Sprintf("%s/%d %d:%d %q", native.GalaTokenName(int32(ttype)), channel, line, col, text)
}

// corpusInputs is every staged source, two broken variants of each (lexical
// errors, unbalanced brackets, invalid UTF-8 where a cut splits a multibyte
// character), about one in 15 with CRLF line ends, and the position inputs.
// Each file's variants are drawn from the file's own text, so they are the
// same whichever files a test lane stages.
func corpusInputs(t testing.TB) []string {
	t.Helper()
	inputs := slices.Clone(positionInputs)
	for _, src := range sourceTexts(t) {
		inputs = append(inputs, fileVariants(src)...)
	}
	return inputs
}

// fileVariants is src and its corpus variants (see corpusInputs), drawn from
// src's own text alone.
func fileVariants(src string) []string {
	h := fnv.New64a()
	h.Write([]byte(src))
	seed := h.Sum64()
	r := rand.New(rand.NewSource(int64(seed)))
	out := []string{src, breakSource(r, src), breakSource(r, src)}
	if seed%15 == 0 {
		out = append(out, strings.ReplaceAll(src, "\n", "\r\n"))
	}
	return out
}

// TestNativeLexCorpus lexes every corpus input (corpusInputs), ten of them
// again with a leading BOM: galop's lexer must not panic, VisitNativeTokens
// must visit the default-channel tokens the lexer gives, and over the corpus
// the line-break rule must re-type tokens and the doc comments must be found
// (too few would mean a pass did nothing). The parser golden holds what the
// lexer gives for the frozen corpus token for token.
func TestNativeLexCorpus(t *testing.T) {
	retyped, documented := 0, 0
	inputs := corpusInputs(t)
	for _, input := range inputs[:10] {
		inputs = append(inputs, "\xef\xbb\xbf"+input) // a UTF-8 BOM
	}
	for k, input := range inputs {
		ts := lexNativeRecovered(galaerr.StripBOM(input))
		if ts == nil {
			t.Errorf("input %d (%.60q): galop's lexer panicked (rerun with GALA_PANIC_STACK=1 for the stack)", k, input)
			continue
		}
		var want, visited []string
		for _, tok := range ts.Tokens {
			if tok.Type == native.GalaEOF {
				break
			}
			want = append(want, tok.Text(ts.Src))
			switch tok.Type {
			case native.GalaNL_LPAREN, native.GalaNL_STAR, native.GalaNL_AMP:
				retyped++
			}
		}
		VisitNativeTokens(input, func(text string) { visited = append(visited, text) })
		if !slices.Equal(want, visited) {
			t.Errorf("input %d (%.60q): VisitNativeTokens visits %q, the lexer gives %q", k, input, visited, want)
		}
		documented += len(nativeDocComments(ts))
	}
	if retyped < 50 || documented < 1000 {
		t.Errorf("only %d tokens re-typed and %d doc comments over the corpus", retyped, documented)
	}
	t.Logf("%d inputs, %d re-typed tokens, %d doc comments", len(inputs), retyped, documented)
}

// TestLexNativeAt: lexing from a line and column, as the transpiler re-parses
// default values and interpolated expressions, starts the first token there,
// a token on a following line at that line's own column, and a line <= 0 at
// 1:0.
func TestLexNativeAt(t *testing.T) {
	ts := lexNative("a +\n  b", 7, 12)
	if got := fmt.Sprintf("%d:%d %d:%d %d:%d", ts.Tokens[0].Line, ts.Tokens[0].Column,
		ts.Tokens[1].Line, ts.Tokens[1].Column, ts.Tokens[2].Line, ts.Tokens[2].Column); got != "7:12 7:14 8:2" {
		t.Errorf("positions %s, want 7:12 7:14 8:2", got)
	}
	for _, at := range [][2]int{{0, 0}, {0, 7}, {-3, 5}} {
		if tok := lexNative("x", at[0], at[1]).Tokens[0]; tok.Line != 1 || tok.Column != 0 {
			t.Errorf("at %v: the first token starts at %d:%d, want 1:0", at, tok.Line, tok.Column)
		}
	}
}

// emptyLineInputs break the blank-line rule, or keep it in unusual ways.
var emptyLineInputs = []string{
	"package main\nval x = 1\n",
	"package main\nimport \"fmt\"\n\nval x = 1\n",
	"package main\n\nimport \"fmt\"\nval x = 1\n",
	"package main\n\nimport \"fmt\"\nimport \"os\"\nval x = 1\n",
	"package main // ünï\nval x = 1\n",
	"package main /* a\n\n */ val x = 1\n",
	"package main\r\n\r\nimport \"fmt\"\r\n\r\nval x = 1\r\n",
	"package main\r\nimport \"fmt\"\r\n\r\nval x = 1\r\n",
	"package main\n\n// é\nimport \"fmt\"\n// ✓\nval x = 1\n",
	"package main\n",
	"package main\n\nimport \"fmt\"\n",
}

// TestExpressionCheckedLeftovers: an expression followed by more tokens is
// not one expression.
func TestExpressionCheckedLeftovers(t *testing.T) {
	for _, src := range []string{"a b", "x +", "f(1))", "", "a\n(b)", "1 @"} {
		_, err := ParseExpressionCheckedAt(src, 2, 3, "test expression")
		if err == nil || IsParserPanic(err) {
			t.Errorf("%q: error %v, want a syntax error", src, err)
		}
	}
}

// TestNativeParseConcurrent parses distinct sources with galop's native lexer
// and parser on many goroutines at once, from a simultaneous start: each tree
// must be the one a parse on its own gives. The parser shares its prediction
// caches across goroutines; the race lane runs this under the race detector.
func TestNativeParseConcurrent(t *testing.T) {
	goroutines := max(8, runtime.GOMAXPROCS(0)*2)
	const iterations = 6
	sources := make([]string, goroutines*iterations)
	for i := range sources {
		sources[i] = buildComplexSource(1000 + i)
	}
	got := make([]string, len(sources))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := g; i < len(sources); i += goroutines {
				root, err := native.ParseSourceFile(lexNative(sources[i], 0, 0))
				if err != nil {
					got[i] = err.Error()
					continue
				}
				got[i] = root.ToStringTree()
				root.Parent() // indexes the tree's parents
			}
		}()
	}
	close(start)
	wg.Wait()
	for i, src := range sources {
		root, err := native.ParseSourceFile(lexNative(src, 0, 0))
		if err != nil {
			t.Fatalf("source %d: %v", i, err)
		}
		if want := root.ToStringTree(); got[i] != want {
			t.Fatalf("source %d: the concurrent parse differs: %s", i, diffAt(want, got[i]))
		}
	}
}

// largestSource is the largest staged source.
func largestSource(b *testing.B) string {
	b.Helper()
	var largest string
	for _, src := range sourceTexts(b) {
		if len(src) > len(largest) {
			largest = src
		}
	}
	return largest
}

// BenchmarkLexRetype lexes the largest staged source with galop's native
// lexer and the line-break rule.
func BenchmarkLexRetype(b *testing.B) {
	src := largestSource(b)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		lexNative(src, 0, 0)
	}
}
