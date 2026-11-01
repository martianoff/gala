package parser

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// lexErrors collects a lexer's errors as "line:col msg".
type lexErrors struct {
	*antlr.DefaultErrorListener
	msgs []string
}

func (l *lexErrors) SyntaxError(_ antlr.Recognizer, _ interface{}, line, column int, msg string, _ antlr.RecognitionException) {
	l.msgs = append(l.msgs, fmt.Sprintf("%d:%d %s", line, column, msg))
}

// antlrTokens lexes src with ANTLR's lexer and the newline token source, from
// line and column col (see seedLexer), and prints every token, the hidden ones
// included, and every lexical error (see printToken).
func antlrTokens(tb testing.TB, src string, line, col int) []string {
	tb.Helper()
	out, _ := antlrLex(tb, src, line, col)
	return out
}

// antlrLex is antlrTokens and the token stream it prints.
func antlrLex(tb testing.TB, src string, line, col int) ([]string, *antlr.CommonTokenStream) {
	tb.Helper()
	errs := &lexErrors{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	stream := antlrStream(tb, src, line, col, errs)
	var out []string
	for _, tok := range stream.GetAllTokens() {
		out = append(out, printToken(tok.GetTokenType(), tok.GetChannel(), tok.GetLine(), tok.GetColumn(), tok.GetText()))
	}
	return append(out, errs.msgs...), stream
}

// antlrStream is src lexed with ANTLR's lexer and the newline token source,
// from line and column col (see seedLexer), every token read; listener, when
// not nil, hears the lexical errors.
func antlrStream(tb testing.TB, src string, line, col int, listener antlr.ErrorListener) *antlr.CommonTokenStream {
	tb.Helper()
	lexer := grammar.NewgalaLexer(antlr.NewInputStream(src))
	isolateLexerCaches(lexer.BaseLexer)
	if !seedLexer(lexer.BaseLexer, line, col) {
		tb.Fatal("cannot seed ANTLR's lexer")
	}
	lexer.RemoveErrorListeners()
	if listener != nil {
		lexer.AddErrorListener(listener)
	}
	stream := newTokenStream(lexer)
	stream.Fill()
	return stream
}

// sourceTexts is the text of every staged source (see gatherSources).
func sourceTexts(tb testing.TB) []string {
	tb.Helper()
	var texts []string
	for _, path := range gatherSources(tb) {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		texts = append(texts, string(data))
	}
	return texts
}

// nativeTokens is antlrTokens for galop's native lexer and RetypeNewlines.
func nativeTokens(src string, line, col int) []string {
	return printNative(lexNative(src, line, col))
}

// printNative prints ts as antlrTokens prints ANTLR's tokens.
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

// firstTokenDiff is "" when want and got are the same, else where they first
// differ.
func firstTokenDiff(want, got []string) string {
	for i := range min(len(want), len(got)) {
		if want[i] != got[i] {
			return fmt.Sprintf("token %d: ANTLR %s, native %s", i, want[i], got[i])
		}
	}
	if len(want) != len(got) {
		return fmt.Sprintf("ANTLR gives %d tokens and errors, native %d", len(want), len(got))
	}
	return ""
}

// corpusInputs is every staged source, two broken variants of each (lexical
// errors, unbalanced brackets), every 15th with CRLF line ends, and the
// position inputs.
func corpusInputs(t *testing.T) []string {
	t.Helper()
	r := rand.New(rand.NewSource(3))
	inputs := slices.Clone(positionInputs)
	for n, src := range sourceTexts(t) {
		inputs = append(inputs, src, breakSource(r, src), breakSource(r, src))
		if n%15 == 0 {
			inputs = append(inputs, strings.ReplaceAll(src, "\n", "\r\n"))
		}
	}
	return inputs
}

// TestNativeVocabularyIsANTLRs: nativeKinds reads galop's native vocabulary,
// which must number the tokens as ANTLR's parser does.
func TestNativeVocabularyIsANTLRs(t *testing.T) {
	grammar.GalaParserInit()
	vocab := &grammar.GalaParserStaticData
	if !slices.Equal(vocab.SymbolicNames, native.GalaSymbolicNames) {
		t.Errorf("symbolic names differ:\nANTLR:  %q\nnative: %q", vocab.SymbolicNames, native.GalaSymbolicNames)
	}
	// ANTLR's literal names stop at the last token with one; galop's run on
	// with empty names to the vocabulary's end.
	literal := native.GalaLiteralNames
	for len(literal) > len(vocab.LiteralNames) && literal[len(literal)-1] == "" {
		literal = literal[:len(literal)-1]
	}
	if !slices.Equal(vocab.LiteralNames, literal) {
		t.Errorf("literal names differ:\nANTLR:  %q\nnative: %q", vocab.LiteralNames, native.GalaLiteralNames)
	}
}

// TestNativeLexMatchesANTLR lexes every corpus input once with galop's native
// lexer and RetypeNewlines and once with ANTLR's lexer and newlineTokenSource:
// the tokens (types after re-typing, channels, lines, columns, text), the
// lexical errors and the doc comments read from the hidden channel must be
// the same. A source with a leading BOM lexes as ParseFile and VisitTokens
// lex it: without the BOM.
func TestNativeLexMatchesANTLR(t *testing.T) {
	retyped, documented := 0, 0
	nl := []string{"NL_LPAREN/", "NL_STAR/", "NL_AMP/"}
	inputs := corpusInputs(t)
	for _, input := range inputs[:10] {
		inputs = append(inputs, "\xef\xbb\xbf"+input) // a UTF-8 BOM
	}
	for k, input := range inputs {
		want, stream := antlrLex(t, galaerr.StripBOM(input), 0, 0)
		ts := lexNative(input, 0, 0)
		got := printNative(ts)
		if d := firstTokenDiff(want, got); d != "" {
			t.Errorf("input %d (%.60q): %s", k, input, d)
		}
		for _, tok := range got {
			for _, p := range nl {
				if strings.HasPrefix(tok, p) {
					retyped++
				}
			}
		}
		wantDocs, gotDocs := extractDocComments(stream.GetAllTokens()), nativeDocComments(ts)
		if fmt.Sprint(wantDocs) != fmt.Sprint(gotDocs) {
			t.Errorf("input %d (%.60q): doc comments differ\nANTLR:  %q\nnative: %q", k, input, wantDocs, gotDocs)
		}
		documented += len(wantDocs)
	}
	// The corpus re-types and documents plenty; too few would mean a pass did
	// nothing.
	if retyped < 50 || documented < 1000 {
		t.Errorf("only %d tokens re-typed and %d doc comments over the corpus", retyped, documented)
	}
	t.Logf("%d re-typed tokens, %d doc comments", retyped, documented)
}

// TestRetypeNewlinesCases lexes the sources of newline_test.go's line-break
// tests both ways, and bodies that put a comment or a line-spanning literal
// next to a re-typed token.
func TestRetypeNewlinesCases(t *testing.T) {
	inputs := lineBreakInputs()
	for _, body := range []string{
		"Println(\"zero\") /* a\nb */ (1, 2)",
		"val a = b\n* p",
		"val a = b\n*/* c */p",
		"val a = b\n*//c\np",
		"val a = b\n*",
		"if (a) x else y\n*p = 1",
	} {
		inputs = append(inputs, lineStartInput(body))
	}
	if len(inputs) < 100 {
		t.Fatalf("only %d inputs: newline_test.go's tables are not reached", len(inputs))
	}
	for _, src := range inputs {
		if d := firstTokenDiff(antlrTokens(t, src, 0, 0), nativeTokens(src, 0, 0)); d != "" {
			t.Errorf("%q: %s", src, d)
		}
	}
}

// TestLexNativeAt: lexing from a line and column, as the transpiler re-parses
// default values and interpolated expressions, gives ANTLR's seeded lexer's
// tokens and positions, a line <= 0 starting at 1:0.
func TestLexNativeAt(t *testing.T) {
	inputs := append(slices.Clone(expressionAtInputs), "a\n(b)", "x /* c\n */ *y", "\"ünï\" + `\n` + é")
	for _, input := range inputs {
		for _, at := range [][2]int{{0, 0}, {0, 7}, {-3, 5}, {1, 0}, {12, 30}, {400, 2}} {
			want, got := antlrTokens(t, input, at[0], at[1]), nativeTokens(input, at[0], at[1])
			if d := firstTokenDiff(want, got); d != "" {
				t.Errorf("%q at %v: %s", input, at, d)
			}
		}
	}
	ts := lexNative("a +\n  b", 7, 12)
	if got := fmt.Sprintf("%d:%d %d:%d %d:%d", ts.Tokens[0].Line, ts.Tokens[0].Column,
		ts.Tokens[1].Line, ts.Tokens[1].Column, ts.Tokens[2].Line, ts.Tokens[2].Column); got != "7:12 7:14 8:2" {
		t.Errorf("positions %s, want 7:12 7:14 8:2", got)
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

// TestCheckEmptyLinesNativeMatchesANTLR: the blank-line check on the native
// tree reports what checkEmptyLines reports on ANTLR's, for every input that
// parses.
func TestCheckEmptyLinesNativeMatchesANTLR(t *testing.T) {
	p := NewAntlrGalaParser()
	checked, rejected := 0, 0
	for k, input := range append(slices.Clone(emptyLineInputs), corpusInputs(t)...) {
		ts := lexNative(input, 0, 0)
		if len(ts.Errors) > 0 {
			continue // ParseFile reports the lexical errors, not the blank lines
		}
		root, err := native.ParseSourceFile(ts)
		if err != nil {
			continue
		}
		// galop's engine builds ANTLR's tree, the same tree as ANTLR's parser
		// for input that parses (TestEnginesAgree), at a fraction of the cost.
		r := parseSourceFile(input, EngineGalop)
		if len(r.errors) > 0 || !r.galop {
			t.Errorf("input %d (%.60q): native parses, galop's ANTLR-tree parse does not: %v", k, input, r.errors)
			continue
		}
		want := fmt.Sprint(p.checkEmptyLines(r.input, r.tree))
		if got := fmt.Sprint(checkEmptyLinesNative(root)); got != want {
			t.Errorf("input %d (%.60q): ANTLR %s, native %s", k, input, want, got)
		}
		checked++
		if want != "<nil>" {
			rejected++
		}
	}
	if rejected < 6 {
		t.Errorf("only %d inputs rejected", rejected)
	}
	t.Logf("%d inputs checked, %d rejected", checked, rejected)
}

// expressionSample is an expression's text and where it sits in its file.
type expressionSample struct {
	src       string
	line, col int
}

// corpusExpressions are the corpus's default values, at their positions, its
// interpolated expressions and token runs at drawn positions, and a broken
// variant of each.
func corpusExpressions(t *testing.T) []expressionSample {
	t.Helper()
	r := rand.New(rand.NewSource(4))
	var out []expressionSample
	add := func(s expressionSample) {
		out = append(out, s, expressionSample{breakSource(r, s.src), s.line, s.col})
	}
	for _, input := range expressionAtInputs {
		add(expressionSample{input, 3, 4})
	}
	defaults := 0
	for _, src := range sourceTexts(t) {
		if root, err := native.ParseSourceFile(lexNative(src, 0, 0)); err == nil {
			root.Walk(func(n native.GalaNodeRef) bool {
				if n.Kind() == native.GalaKIND_paramDefault {
					e := native.ParamDefaultNode{GalaNodeRef: n}.Expression()
					start := e.Start()
					add(expressionSample{e.SourceText(), int(start.Line), int(start.Column)})
					defaults++
				}
				return true
			})
		}
		for _, m := range interpolation.FindAllStringSubmatch(src, -1) {
			add(expressionSample{m[1], 1 + r.Intn(500), r.Intn(80)})
		}
		if toks := lexed(src); len(toks) > 1 {
			i := r.Intn(len(toks))
			j := min(len(toks), i+1+r.Intn(12))
			add(expressionSample{src[toks[i].GetStart() : toks[j-1].GetStop()+1], 1 + r.Intn(500), r.Intn(80)})
		}
	}
	if defaults < 50 {
		t.Fatalf("only %d default values in the corpus", defaults)
	}
	return out
}

// antlrTerminals prints the terminals under tree with their positions.
func antlrTerminals(b *strings.Builder, tree antlr.Tree) {
	if n, ok := tree.(antlr.TerminalNode); ok {
		tok := n.GetSymbol()
		fmt.Fprintf(b, " %s@%d:%d", native.GalaTokenName(int32(tok.GetTokenType())), tok.GetLine(), tok.GetColumn())
		return
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		antlrTerminals(b, tree.GetChild(i))
	}
}

// nativeTerminals is antlrTerminals for a native node.
func nativeTerminals(b *strings.Builder, n native.GalaNodeRef) {
	for i := range n.ChildCount() {
		c := n.Child(i)
		if c.IsToken() {
			tok := c.Terminal().Token()
			fmt.Fprintf(b, " %s@%d:%d", native.GalaTokenName(tok.Type), tok.Line, tok.Column)
		} else {
			nativeTerminals(b, c.Node())
		}
	}
}

// TestParseExpressionNativeAtMatchesANTLR: over the corpus's expressions,
// ParseExpressionNativeAt accepts exactly what ParseExpressionAt with ANTLR's
// engine accepts, with the same tree and token positions, and fails with
// ErrNativeExpression on the rest.
func TestParseExpressionNativeAtMatchesANTLR(t *testing.T) {
	accepted, rejected := 0, 0
	samples := corpusExpressions(t)
	withEngine(EngineANTLR, func() {
		for _, s := range samples {
			want, wantErr := ParseExpressionAt(s.src, s.line, s.col, "test expression")
			got, err := ParseExpressionNativeAt(s.src, s.line, s.col)
			switch {
			case err != nil && !errors.Is(err, ErrNativeExpression):
				t.Errorf("%q at %d:%d: error %v does not wrap ErrNativeExpression", s.src, s.line, s.col, err)
			case (err == nil) != (wantErr == nil):
				t.Errorf("%q at %d:%d: ANTLR error %v, native error %v", s.src, s.line, s.col, wantErr, err)
			case err != nil:
				rejected++
			default:
				accepted++
				if w, g := treeText(want), got.ToStringTree(); w != g {
					t.Errorf("%q at %d:%d: trees differ: %s", s.src, s.line, s.col, diffAt(w, g))
				}
				var w, g strings.Builder
				antlrTerminals(&w, want)
				nativeTerminals(&g, got.GalaNodeRef)
				if w.String() != g.String() {
					t.Errorf("%q at %d:%d: token positions differ: %s", s.src, s.line, s.col, diffAt(w.String(), g.String()))
				}
			}
		}
	})
	if accepted < 500 || rejected < 100 {
		t.Errorf("%d expressions accepted, %d rejected: the sample is too thin", accepted, rejected)
	}
	t.Logf("%d expressions accepted, %d rejected", accepted, rejected)
}

// TestParseExpressionNativeAtLeftovers: an expression followed by more tokens
// is not one expression.
func TestParseExpressionNativeAtLeftovers(t *testing.T) {
	for _, src := range []string{"a b", "x +", "f(1))", "", "a\n(b)", "1 @"} {
		if _, err := ParseExpressionNativeAt(src, 2, 3); !errors.Is(err, ErrNativeExpression) {
			t.Errorf("%q: error %v, want ErrNativeExpression", src, err)
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

// BenchmarkLexRetype compares lexing the largest staged source with galop's
// native lexer and RetypeNewlines against ANTLR's lexer and
// newlineTokenSource.
func BenchmarkLexRetype(b *testing.B) {
	src := largestSource(b)
	b.Run("native", func(b *testing.B) {
		b.SetBytes(int64(len(src)))
		for b.Loop() {
			lexNative(src, 0, 0)
		}
	})
	b.Run("antlr", func(b *testing.B) {
		b.SetBytes(int64(len(src)))
		for b.Loop() {
			antlrStream(b, src, 0, 0, nil)
		}
	})
}
