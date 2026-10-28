package parser

import (
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/parser/galop"
	"martianoff/gala/internal/parser/grammar"
)

// gatherSources returns the .gala files staged as data of parser_test.
func gatherSources(t *testing.T) []string {
	t.Helper()
	files, err := bazel.ListRunfiles()
	if err != nil {
		t.Fatalf("cannot list the runfiles: %v", err)
	}
	var paths []string
	for _, f := range files {
		if strings.HasSuffix(f.ShortPath, ".gala") {
			paths = append(paths, f.Path)
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
	SetEngine(e)
	defer chosen.Store(saved)
	f()
}

// render prints what a parse gives: the tree, then the doc comments and the
// errors.
func render(tree antlr.Tree, docs map[int]string, errs []error) string {
	var b strings.Builder
	if tree != nil {
		b.WriteString(antlr.TreesStringTree(tree, grammar.GalaParserStaticData.RuleNames, nil))
	}
	lines := make([]int, 0, len(docs))
	for line := range docs {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	for _, line := range lines {
		fmt.Fprintf(&b, "\ndoc %d: %q", line, docs[line])
	}
	for _, err := range errs {
		fmt.Fprintf(&b, "\nerror: %v", err)
	}
	return b.String()
}

func parseFileWith(e Engine, src string) (out string) {
	withEngine(e, func() { out = render(NewAntlrGalaParser().ParseLenient(src)) })
	return out
}

func parseExpressionWith(e Engine, src string) (out string) {
	withEngine(e, func() {
		expr, errs := NewAntlrGalaParser().ParseExpression(src)
		var tree antlr.Tree
		if expr != nil {
			tree = expr
		}
		out = render(tree, nil, errs)
	})
	return out
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

// TestEnginesAgree parses every GALA source of the repository, and two broken
// variants of each, with galop's engine and with ANTLR's: the trees, doc
// comments and errors must be the same. It does the same for expressions:
// the corpus's interpolated expressions, runs of tokens from every file, and
// broken variants of both. galop's parser builds ANTLR's tree on input that
// parses and leaves the rest to ANTLR, so any difference is a bug.
func TestEnginesAgree(t *testing.T) {
	if !galop.BuiltIn {
		t.Skip("galop's parser is not built in")
	}
	grammar.GalaParserInit()
	r := rand.New(rand.NewSource(1))
	files, accepted, exprs, exprsAccepted := 0, 0, 0, 0
	for _, path := range gatherSources(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for k, input := range []string{src, breakSource(r, src), breakSource(r, src)} {
			want, got := parseFileWith(EngineANTLR, input), parseFileWith(EngineGalop, input)
			if got != want {
				t.Errorf("%s (variant %d): the engines disagree\nANTLR: %.300s\ngalop: %.300s", path, k, want, got)
			}
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
					t.Errorf("%s: expression %q (variant %d): the engines disagree\nANTLR: %.300s\ngalop: %.300s", path, expr, k, want, got)
				}
				exprs++
				if !strings.Contains(want, "error:") {
					exprsAccepted++
				}
			}
		}
	}
	t.Logf("%d source inputs (%d without errors), %d expressions (%d without errors)", files, accepted, exprs, exprsAccepted)
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

// TestGalopPanicFallsBack makes galop's parser panic: the parse must still
// succeed, with ANTLR's result.
func TestGalopPanicFallsBack(t *testing.T) {
	if !galop.BuiltIn {
		t.Skip("galop's parser is not built in")
	}
	savedFile, savedExpr := galopSourceFile, galopExpression
	defer func() { galopSourceFile, galopExpression = savedFile, savedExpr }()
	galopSourceFile = func(*antlr.CommonTokenStream) (grammar.ISourceFileContext, bool) { panic("injected") }
	galopExpression = func(*antlr.CommonTokenStream) (grammar.IExpressionContext, bool) { panic("injected") }

	src := "package main\n\nfunc main() {\n    Println(\"hi\")\n}\n"
	want := parseFileWith(EngineANTLR, src)
	if got := parseFileWith(EngineGalop, src); got != want {
		t.Errorf("source file after a panic:\n got: %s\nwant: %s", got, want)
	}
	if strings.Contains(want, "error:") {
		t.Fatalf("the source does not parse: %s", want)
	}
	if got, want := parseExpressionWith(EngineGalop, "a + b"), parseExpressionWith(EngineANTLR, "a + b"); got != want {
		t.Errorf("expression after a panic:\n got: %s\nwant: %s", got, want)
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
	if galop.BuiltIn {
		RequestEngine("galop")
		if e, err := currentEngine(); err != nil || e != EngineGalop {
			t.Fatalf("--parser=galop over GALA_PARSER=antlr: engine %q, error %v", e, err)
		}
	}
	RequestEngine("yacc")
	_, _, errs := NewAntlrGalaParser().ParseLenient("package main\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `unknown parser "yacc"`) {
		t.Fatalf("--parser=yacc: errors %v", errs)
	}
}

// TestBuiltInDefault checks the engine a build defaults to: galop's where it
// is built in. CI's job for ANTLR's parser builds with --//internal/parser:antlr
// and expects ANTLR's through GALA_EXPECT_DEFAULT_ENGINE.
func TestBuiltInDefault(t *testing.T) {
	if os.Getenv("GALA_PARSER") != "" {
		t.Skip("GALA_PARSER is set")
	}
	want := os.Getenv("GALA_EXPECT_DEFAULT_ENGINE")
	if want == "" {
		want = string(EngineANTLR)
		if galop.BuiltIn {
			want = string(EngineGalop)
		}
	}
	saved := chosen.Load()
	defer chosen.Store(saved)
	RequestEngine("")
	defer RequestEngine("")
	if e, err := currentEngine(); err != nil || string(e) != want {
		t.Fatalf("the default engine is %q (error %v), want %q", e, err, want)
	}
}
