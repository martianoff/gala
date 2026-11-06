package parser

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// gatherSources returns the .gala files staged as data of parser_test; under
// plain `go test` (tools/fuzz.sh), which stages no runfiles, the repository's
// own .gala files.
func gatherSources(t testing.TB) []string {
	t.Helper()
	var paths []string
	if os.Getenv("TEST_SRCDIR") == "" {
		paths = repoSources(t)
	} else {
		files, err := bazel.ListRunfiles()
		if err != nil {
			t.Fatalf("cannot list the runfiles: %v", err)
		}
		for _, f := range files {
			if strings.HasSuffix(f.ShortPath, ".gala") {
				paths = append(paths, f.Path)
			}
		}
	}
	sort.Strings(paths)
	if len(paths) < 200 {
		t.Fatalf("found %d .gala files in the runfiles; the sources are not staged", len(paths))
	}
	return paths
}

// withEngine runs f with engine e, then restores the engine.
func withEngine(e Engine, f func()) {
	saved := chosen.Load()
	chosen.Store(&engineChoice{engine: e})
	defer chosen.Store(saved)
	f()
}

// treeText is tree in ANTLR's LISP-like form, "" for no tree.
func treeText(tree antlr.Tree) string {
	if tree == nil {
		return ""
	}
	return antlr.TreesStringTree(tree, grammar.GalaParserStaticData.RuleNames, nil)
}

// renderText prints what a parse gives: text, the tree's text (treeText),
// and the tree with every node's position (see positions), then the doc
// comments and the errors.
func renderText(text string, tree antlr.Tree, docs Docs, errs []error) string {
	var b strings.Builder
	if tree != nil {
		b.WriteString(text)
		positions(&b, tree, nil)
	}
	keys := make([]DocKey, 0, len(docs))
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Line < keys[j].Line || keys[i].Line == keys[j].Line && keys[i].Column < keys[j].Column
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "\ndoc %d:%d: %q", k.Line, k.Column, docs[k])
	}
	for _, err := range errs {
		fmt.Fprintf(&b, "\nerror: %v", err)
	}
	return b.String()
}

// fileParse is a parse of a source file with one engine, and its rendering.
type fileParse struct {
	parsed *Parsed
	text   string // treeText of the parse's ANTLR tree, "" for none
	out    string // render of the parse
	// native is dumpNative of the parse's clean native tree, "" for none.
	native string
	// recovered is dumpNative of the native tree of a parse with errors (see
	// Parsed.RecoveredNative), "" for none.
	recovered string
}

// parseFileAs parses src with engine e. A parse's native tree is converted
// (with ANTLR's engine, or for a parse with errors) and printed.
func parseFileAs(e Engine, src string) fileParse {
	var f fileParse
	withEngine(e, func() {
		f.parsed = NewAntlrGalaParser().ParseFile(src)
		f.text = treeText(f.parsed.Tree)
		f.out = renderText(f.text, f.parsed.Tree, f.parsed.Docs, f.parsed.Errors)
		tree, err := f.parsed.RecoveredNative().Tree()
		switch {
		case err != nil:
		case len(f.parsed.Errors) == 0:
			f.native = dumpNative(tree)
		default:
			f.recovered = dumpNative(tree)
		}
	})
	return f
}

// docsAndErrorsDiff is "" when want and got give the same doc comments and
// errors, else where they first differ.
func docsAndErrorsDiff(want, got fileParse) string {
	w := renderText("", nil, want.parsed.Docs, want.parsed.Errors)
	if g := renderText("", nil, got.parsed.Docs, got.parsed.Errors); g != w {
		return diffAt(w, g)
	}
	return ""
}

// diffAt shows where want and got first differ: the line number, and each
// side's text around the first differing byte of that line (tree text is one
// long line, so a whole line can be too long to read).
func diffAt(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	// i is at most either length; show from up to 120 bytes before it, on its
	// line, to up to 240 bytes after it, on the same line.
	from := max(strings.LastIndexByte(want[:i], '\n')+1, i-120)
	window := func(s string) string {
		end := min(len(s), i+240)
		if nl := strings.IndexByte(s[i:end], '\n'); nl >= 0 {
			end = i + nl
		}
		return fmt.Sprintf("%q", s[from:end])
	}
	line := strings.Count(want[:i], "\n") + 1
	return fmt.Sprintf("first difference at line %d, byte %d\n  want: %s\n  got:  %s", line, i, window(want), window(got))
}

// renderExpression prints a native expression tree (dumpNative and its
// LISP-like form) and errors.
func renderExpression(expr native.ExpressionNode, errs []error) string {
	var tree string
	if expr.Ok() {
		tree = expr.ToStringTree() + "\n" + dumpNative(expr.Tree())
	}
	return tree + renderText("", nil, nil, errs)
}

// parseExpressionWith parses src with ParseExpressionTree with engine e.
func parseExpressionWith(e Engine, src string) string {
	return parseExpressionUsing(galopParsers, e, src)
}

// parseExpressionUsing is parseExpressionWith with g's parser and converter.
func parseExpressionUsing(g *nativeParsers, e Engine, src string) (out string) {
	withEngine(e, func() {
		out = renderExpression(g.expressionTree(src))
	})
	return out
}

// parseExpressionAtWith parses src with ParseExpressionCheckedAt, at line and
// col, with engine e.
func parseExpressionAtWith(e Engine, src string, line, col int) string {
	return parseExpressionAtUsing(galopParsers, e, src, line, col)
}

// parseExpressionAtUsing is parseExpressionAtWith with g's parser and
// converter.
func parseExpressionAtUsing(g *nativeParsers, e Engine, src string, line, col int) (out string) {
	withEngine(e, func() {
		expr, _, err := g.expressionCheckedAt(src, line, col, "test expression")
		var errs []error
		if err != nil {
			errs = []error{err}
		}
		out = renderExpression(expr, errs)
	})
	return out
}

// positions prints, for every node under tree, what diagnostics, hover and
// line markers read from it: a rule's first and last token indexes, a token's
// type, channel, line, column, character offsets and index, and whether the
// node's parent link points at the node it hangs from.
func positions(b *strings.Builder, tree antlr.Tree, parent antlr.Tree) {
	if parent != nil && tree.GetParent() != parent {
		fmt.Fprintf(b, "\nwrong parent link under %T", parent)
	}
	switch n := tree.(type) {
	case antlr.TerminalNode:
		tok := n.GetSymbol()
		fmt.Fprintf(b, "\ntoken %d/%d %d:%d [%d,%d] #%d", tok.GetTokenType(), tok.GetChannel(),
			tok.GetLine(), tok.GetColumn(), tok.GetStart(), tok.GetStop(), tok.GetTokenIndex())
	case antlr.ParserRuleContext:
		fmt.Fprintf(b, "\nrule %d", n.GetRuleIndex())
		if start := n.GetStart(); start != nil {
			fmt.Fprintf(b, " from #%d", start.GetTokenIndex())
		}
		if stop := n.GetStop(); stop != nil {
			fmt.Fprintf(b, " to #%d", stop.GetTokenIndex())
		}
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		positions(b, tree.GetChild(i), tree)
	}
}

// positionInputs exercise what the lexers count positions over: non-ASCII
// text, CRLF line ends and comments that span lines.
var positionInputs = []string{
	"package main\n\n/* a block comment\n   over three lines, with ünïcödé ✓\n*/\nfunc main() {\n    val s = \"héllo wörld ✓ 日本\" // trailing — comment\n    Println(s)\n}\n",
	"package main\r\n\r\n/* block\r\n   comment */\r\nfunc main() {\r\n    val s = \"ä\"\r\n    Println(s + s\"${s}é\")\r\n}\r\n",
	"package main\n\n// ünïcödé line comment\nfunc f(a int) int = a /* inline\n multi-line ✓ */ + 1\n",
}

// breakSource drops a few bytes of src somewhere: mostly a syntax error, at
// times still valid code.
func breakSource(r *rand.Rand, src string) string {
	i := r.Intn(len(src) + 1)
	j := min(len(src), i+1+r.Intn(8))
	return src[:i] + src[j:]
}

// interpolation matches the ${...} parts of interpolated strings, whose text
// the transpiler re-parses as expressions.
var interpolation = regexp.MustCompile(`\$\{([^{}"]+)\}`)

// compareEngines parses input with ANTLR's engine and with galop's, and
// returns ANTLR's parse, whether galop's engine settled a parse whose only
// error is a missing blank line, and every way the two differ. The doc
// comments and errors must be the same; a parse without errors must have the
// same native tree (galop's native parse with galop's engine, ANTLR's tree
// converted with ANTLR's), and no ANTLR tree with galop's engine; a parse
// with errors must be ANTLR's with both, the same error-recovered tree, but
// for one whose only error is a missing blank line, which galop's engine
// leaves to its native parse; either way the recovered native trees must be
// the same. When check is set, galop's engine's native tree, clean or
// error-recovered, is also checked against ANTLR's tree (checkNative).
func compareEngines(input string, check bool) (want fileParse, blankLineOnly bool, diffs []string) {
	want, got := parseFileAs(EngineANTLR, input), parseFileAs(EngineGalop, input)
	if len(want.parsed.Errors) > 0 {
		if got.parsed.Tree == nil {
			// galop's native parse settled it: a missing blank line is its
			// only error.
			blankLineOnly = true
			if d := docsAndErrorsDiff(want, got); d != "" {
				diffs = append(diffs, "the engines disagree on the doc comments or errors: "+d)
			}
		} else if got.out != want.out {
			// Both are ANTLR's parse, error-recovered.
			diffs = append(diffs, "the engines disagree (want ANTLR's, got galop's): "+diffAt(want.out, got.out))
		}
		if got.recovered != want.recovered {
			diffs = append(diffs, "the recovered native trees differ: "+diffAt(want.recovered, got.recovered))
		}
	} else {
		if d := docsAndErrorsDiff(want, got); d != "" {
			diffs = append(diffs, "the engines disagree on the doc comments or errors: "+d)
		}
		switch {
		case want.native == "":
			diffs = append(diffs, "the clean parse with ANTLR's engine has no native tree")
		case got.native == "":
			diffs = append(diffs, "galop's clean parse has no native tree")
		case got.parsed.Tree != nil:
			diffs = append(diffs, "galop's clean parse has an ANTLR tree")
		case got.native != want.native:
			diffs = append(diffs, "the native trees differ (want ANTLR's converted, got galop's): "+diffAt(want.native, got.native))
		}
	}
	if check {
		if msg := checkNative(got.parsed, input); msg != "" {
			diffs = append(diffs, "native tree: "+msg)
		}
	}
	return want, blankLineOnly, diffs
}

// TestEnginesAgree parses every GALA source of the repository, and two broken
// variants of each, with galop's engine and with ANTLR's, and holds them to
// compareEngines. It does the same for expressions: the corpus's
// interpolated expressions, runs of tokens from every file, and broken
// variants of both. Any difference is a bug. FuzzParserEngines holds
// arbitrary input to the same checks.
func TestEnginesAgree(t *testing.T) {
	grammar.GalaParserInit()
	// r draws the broken variants and token runs; positions draws where
	// ParseExpressionCheckedAt places an expression, from its own stream so that the
	// samples r draws stay the same.
	r := rand.New(rand.NewSource(1))
	positions := rand.New(rand.NewSource(2))
	files, accepted, blankLineOnly, exprs, exprsAccepted, exprsAtAccepted, crlf := 0, 0, 0, 0, 0, 0, 0
	// compareFile parses input with both engines and reports every
	// difference (see compareEngines). It returns ANTLR's rendering.
	compareFile := func(what, input string, check bool) string {
		want, blankOnly, diffs := compareEngines(input, check)
		if blankOnly {
			blankLineOnly++
		}
		for _, d := range diffs {
			t.Errorf("%s: %s", what, d)
		}
		return want.out
	}
	for k, input := range positionInputs {
		want := compareFile(fmt.Sprintf("position input %d", k), input, true)
		if strings.Contains(want, "error:") {
			t.Errorf("position input %d does not parse: %.300s", k, want)
		}
	}
	compareExpressionAt := func(what, input string, line, col int) bool {
		want, got := parseExpressionAtWith(EngineANTLR, input, line, col), parseExpressionAtWith(EngineGalop, input, line, col)
		if got != want {
			t.Errorf("%s at %d:%d: the engines disagree: %s", what, line, col, diffAt(want, got))
		}
		return !strings.Contains(want, "error:")
	}
	for k, input := range expressionAtInputs {
		for _, at := range [][2]int{{0, 0}, {0, 7}, {-3, 5}, {12, 30}} {
			if !compareExpressionAt(fmt.Sprintf("expression input %d", k), input, at[0], at[1]) {
				t.Errorf("expression input %d does not parse at %v", k, at)
			}
		}
	}
	sources := gatherSources(t)
	for n, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		inputs := []string{src, breakSource(r, src), breakSource(r, src)}
		if n%15 == 0 {
			// Every 15th file also with CRLF line ends.
			inputs = append(inputs, strings.ReplaceAll(src, "\n", "\r\n"))
			crlf++
		}
		for k, input := range inputs {
			// checkNative on each file's source, and on every tenth file's
			// first broken variant, for error-recovered trees: checking each
			// variant would double the suite's time for little more coverage.
			check := k == 0 || k == 1 && n%10 == 0
			want := compareFile(fmt.Sprintf("%s (variant %d)", path, k), input, check)
			files++
			if !strings.Contains(want, "\nerror:") {
				accepted++
			}
		}
		var candidates []string
		for _, m := range interpolation.FindAllStringSubmatch(src, -1) {
			candidates = append(candidates, m[1])
		}
		if toks := lexed(src); len(toks) > 1 {
			for range 4 {
				i := r.Intn(len(toks))
				j := min(len(toks), i+1+r.Intn(12))
				candidates = append(candidates, src[toks[i].GetStart():toks[j-1].GetStop()+1])
			}
		}
		for _, expr := range candidates {
			for k, input := range []string{expr, breakSource(r, expr)} {
				want, got := parseExpressionWith(EngineANTLR, input), parseExpressionWith(EngineGalop, input)
				if got != want {
					t.Errorf("%s: expression %q (variant %d): the engines disagree: %s", path, expr, k, diffAt(want, got))
				}
				exprs++
				if !strings.Contains(want, "error:") {
					exprsAccepted++
				}
				// As the transpiler re-parses it: at a position in its file.
				if compareExpressionAt(fmt.Sprintf("%s: expression %q (variant %d)", path, expr, k),
					input, 1+positions.Intn(500), positions.Intn(80)) {
					exprsAtAccepted++
				}
			}
		}
	}
	if want := (len(sources) + 14) / 15; crlf != want {
		t.Errorf("%d files compared with CRLF line ends, want %d", crlf, want)
	}
	t.Logf("%d source inputs (%d without errors, %d with a missing blank line alone, %d CRLF copies); %d expressions, %d without errors through ParseExpressionTree and %d through ParseExpressionCheckedAt",
		files, accepted, blankLineOnly, crlf, exprs, exprsAccepted, exprsAtAccepted)
}

// expressionAtInputs are expressions placed by ParseExpressionCheckedAt, among them
// ones that span lines, where the starting column applies to the first line
// only (a multi-line default value).
var expressionAtInputs = []string{
	"a + b",
	"Array(1,\n    2,\n    3)",
	"Config(\n    name = \"ünïcödé\",\n    retries = 3,\n)",
	"xs.Map((x) => x *\n    2)",
	"s\"${a}\\n${b}\"",
}

// lexed returns the default-channel tokens of src, without EOF. Token offsets
// count code points, so callers index src only when it is ASCII.
func lexed(src string) []antlr.Token {
	for _, c := range src {
		if c > 127 {
			return nil
		}
	}
	var toks []antlr.Token
	VisitTokens(src, func(tok antlr.Token) {
		if tok.GetChannel() == antlr.TokenDefaultChannel {
			toks = append(toks, tok)
		}
	})
	return toks
}

// TestParseExpressionTreeReportsConversionFailure: when galop's converter
// gives no tree, ParseExpressionTree gives the zero node and says why
// (ErrConvertExpression), whether the converter returned nothing or panicked.
func TestParseExpressionTreeReportsConversionFailure(t *testing.T) {
	RequestEngine(string(EngineANTLR))
	defer RequestEngine("")
	for name, conv := range map[string]func(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) *native.GalaTree{
		"no tree": func(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) *native.GalaTree {
			return nil
		},
		"panic": func(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) *native.GalaTree {
			panic("injected")
		},
	} {
		faulty := *galopParsers
		faulty.toNative = conv
		expr, errs := faulty.expressionTree("a + b")
		if expr.Ok() {
			t.Errorf("%s: got a tree", name)
		}
		found := false
		for _, err := range errs {
			found = found || errors.Is(err, ErrConvertExpression)
		}
		if !found {
			t.Errorf("%s: errors %v do not report the conversion failure", name, errs)
		}
	}
	if expr, errs := ParseExpressionTree("a + b"); !expr.Ok() || len(errs) > 0 {
		t.Errorf("with galop's converter: tree %v, errors %v", expr.Ok(), errs)
	}
}

// TestEngineSelection covers the --parser flag (RequestEngine), GALA_PARSER,
// and an unknown name, which fails parses but nothing else.
func TestEngineSelection(t *testing.T) {
	saved := chosen.Load()
	defer chosen.Store(saved)
	defer RequestEngine("")

	t.Setenv("GALA_PARSER", "antlr")
	RequestEngine("")
	if e, err := currentEngine(); err != nil || e != EngineANTLR {
		t.Fatalf("GALA_PARSER=antlr: engine %q, error %v", e, err)
	}
	RequestEngine("galop")
	if e, err := currentEngine(); err != nil || e != EngineGalop {
		t.Fatalf("--parser=galop over GALA_PARSER=antlr: engine %q, error %v", e, err)
	}
	RequestEngine("yacc")
	errs := NewAntlrGalaParser().ParseFile("package main\n").Errors
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `unknown parser "yacc"`) {
		t.Fatalf("--parser=yacc: errors %v", errs)
	}
}

// TestDefaultEngine checks the engine a build defaults to: galop's, unless it
// is built with --//internal/parser:antlr. CI's job for ANTLR's parser builds
// so and expects ANTLR's through GALA_EXPECT_DEFAULT_ENGINE.
func TestDefaultEngine(t *testing.T) {
	if os.Getenv("GALA_PARSER") != "" {
		t.Skip("GALA_PARSER is set")
	}
	want := os.Getenv("GALA_EXPECT_DEFAULT_ENGINE")
	if want == "" {
		want = string(EngineGalop)
	}
	saved := chosen.Load()
	defer chosen.Store(saved)
	RequestEngine("")
	defer RequestEngine("")
	if e, err := currentEngine(); err != nil || string(e) != want {
		t.Fatalf("the default engine is %q (error %v), want %q", e, err, want)
	}
}

func TestIsPowerOf10(t *testing.T) {
	for n, want := range map[int64]bool{1: true, 2: false, 10: true, 11: false, 100: true, 1000: true, 1010: false, 20: false} {
		if got := isPowerOf10(n); got != want {
			t.Errorf("isPowerOf10(%d) = %v, want %v", n, got, want)
		}
	}
}
