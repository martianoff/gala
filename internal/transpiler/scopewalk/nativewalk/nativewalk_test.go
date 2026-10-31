package nativewalk

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// TestMain parses with galop's engine, whatever the build's default: only its
// parses have a native tree to walk.
func TestMain(m *testing.M) {
	parser.RequestEngine(string(parser.EngineGalop))
	os.Exit(m.Run())
}

// recorder writes down everything a walk reports, in order: each reference
// with its site and use, and each call of the EnterFunctionScope hook.
type recorder struct{ events []string }

var (
	_ scopewalk.Visitor = (*recorder)(nil)
	_ scopewalk.Visitor = discard{}
)

func (r *recorder) Reference(name string, at scopewalk.Site, use scopewalk.Use) {
	r.events = append(r.events, fmt.Sprintf("%s at %d:%d %q %+v", name, at.Line, at.Column, at.Text, use))
}

// optionSet is one set of options the walks are compared under: the options,
// whose EnterFunctionScope (if any) is ANTLR's walk's hook, and enter, the
// native walk's.
type optionSet struct {
	opts  scopewalk.Options
	enter EnterFunctionScope
}

// optionSets are the option sets the walks are compared under: the capture
// analysis's, the undefined-symbol check's, and the blunt pattern binding.
// The hooks record their call, ANTLR's to a and the native one to n, and bind
// the receiver's identifiers, as the analyzer's do, so a hook handed the
// wrong declaration changes what follows.
func optionSets(a, n *recorder) []optionSet {
	hook := func(w *scopewalk.Walker, fn grammar.IFunctionDeclarationContext) {
		start := fn.GetStart()
		a.enter(fn.Identifier().GetText(), start.GetLine(), start.GetColumn())
		if recv := fn.Receiver(); recv != nil {
			w.BindIdentifiersIn(recv)
		}
	}
	enter := func(w *scopewalk.Walker, fn native.FunctionDeclarationNode) {
		start := fn.Start()
		n.enter(parser.NativeText(fn.Identifier().GalaNodeRef), int(start.Line), int(start.Column))
		BindIdentifiersIn(w, fn.Receiver().GalaNodeRef)
	}
	return []optionSet{
		{opts: scopewalk.Options{ParseInterpolations: true}},
		{opts: scopewalk.Options{
			ParseInterpolations:            true,
			RequireCleanInterpolationParse: true,
			SkipCompositeLiteralKeys:       true,
			SkipTypedPatternArgument:       true,
			EnterFunctionScope:             hook,
		}, enter: enter},
		{opts: scopewalk.Options{BindWholePattern: true, EnterFunctionScope: hook}, enter: enter},
	}
}

// enter records a call of the hook, on the function named name at line:col.
func (r *recorder) enter(name string, line, col int) {
	r.events = append(r.events, fmt.Sprintf("enter %s at %d:%d", name, line, col))
}

// walkBoth walks ctx, a context of p, on ANTLR's tree and its node on the
// native tree under every option set, and returns where the two reports
// first differ ("" when they agree). A parameter list is walked as
// WalkParameterDefaults walks it, anything else as Walk does. Each walk has
// its own recorder, so its own hook.
func walkBoth(p *parser.Parsed, ctx antlr.ParserRuleContext) string {
	node := p.Native.Node(ctx)
	if !node.Ok() {
		return "the context has no native node"
	}
	params, isParams := ctx.(*grammar.ParametersContext)
	var a, n recorder
	for i, set := range optionSets(&a, &n) {
		a.events, n.events = nil, nil
		wa := scopewalk.New(&a, set.opts)
		wn := scopewalk.New(&n, set.opts)
		nw := New(wn, set.enter)
		wa.PushScope()
		wn.PushScope()
		if isParams {
			wa.WalkParameterDefaults(params)
			nw.WalkParameterDefaults(native.ParametersNode{GalaNodeRef: node})
		} else {
			wa.Walk(ctx)
			nw.Walk(node)
		}

		if !slices.Equal(a.events, n.events) {
			j := 0
			for j < len(a.events) && j < len(n.events) && a.events[j] == n.events[j] {
				j++
			}
			return fmt.Sprintf("options %d, event %d: ANTLR's walk reports %q, the native walk %q", i, j, at(a.events, j), at(n.events, j))
		}
	}
	return ""
}

// at is events[j], or "" past its end.
func at(events []string, j int) string {
	if j < len(events) {
		return events[j]
	}
	return ""
}

// entriesIn is every context of tree the walk's callers start from, besides
// the root: the lambdas (the capture analysis), and the expression lists and
// parameter lists (the undefined-symbol check's top-level vals and struct
// shorthands).
func entriesIn(tree antlr.Tree, out []antlr.ParserRuleContext) []antlr.ParserRuleContext {
	switch c := tree.(type) {
	case *grammar.LambdaExpressionContext:
		out = append(out, c)
	case *grammar.ExpressionListContext:
		out = append(out, c)
	case *grammar.ParametersContext:
		out = append(out, c)
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		out = entriesIn(tree.GetChild(i), out)
	}
	return out
}

// walkInputs exercise what positions are counted over (non-ASCII text before
// a name, CRLF line ends) and the walk's less common forms. The CRLF input is
// spelled out: a raw string cannot hold its carriage returns.
var walkInputs = []string{
	`package main

func f(p int) string {
    val s = "héllo ✓ 日本"; val t = s"ü${p + q}ö$r"
    return s + t + u
}
`,
	"package main\r\n\r\nfunc f(p int) int {\r\n    for k = range lookup(\":=\") { Println(k) }\r\n    for i, v := range xs { Println(i + v) }\r\n    return p match {\r\n        case Some(x: int) if x > lim => x\r\n        case Pair(a, b...) => a\r\n        case _ => g(name = p, h...)\r\n    }\r\n}\r\n",
	`package main

func (b *Box[T]) m(x T = dflt) T = T{Field: x, Other: y}.v.w(z)

val h = { case n => n + k }

struct Point(x int = origin, y int = x)
`,
}

// corpus is the paths of the GALA sources staged as the test's data.
func corpus(tb testing.TB) []string {
	files, err := bazel.ListRunfiles()
	if err != nil {
		tb.Fatalf("cannot list the runfiles: %v", err)
	}
	var paths []string
	for _, f := range files {
		if strings.HasSuffix(f.ShortPath, ".gala") {
			paths = append(paths, f.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

// discard is a Visitor that drops what it is told.
type discard struct{}

func (discard) Reference(string, scopewalk.Site, scopewalk.Use) {}

// BenchmarkWalk walks every staged source from its root with the
// undefined-symbol check's options: on ANTLR's tree ("antlr"), on the native
// tree converted within the timed walk, as the transpiler does ("native"),
// and on the native tree converted beforehand ("native-walk"). Each iteration
// parses the sources afresh, untimed.
func BenchmarkWalk(b *testing.B) {
	var texts []string
	for _, path := range corpus(b) {
		text, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		texts = append(texts, string(text))
	}
	opts := scopewalk.Options{ParseInterpolations: true, RequireCleanInterpolationParse: true, SkipCompositeLiteralKeys: true, SkipTypedPatternArgument: true}
	for _, mode := range []string{"antlr", "native", "native-walk"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				parses := make([]*parser.Parsed, 0, len(texts))
				for _, text := range texts {
					if p := parser.NewAntlrGalaParser().ParseFile(text); p.Err() == nil {
						if mode == "native-walk" {
							p.Native.Tree()
						}
						parses = append(parses, p)
					}
				}
				b.StartTimer()
				for _, p := range parses {
					w := scopewalk.New(discard{}, opts)
					if mode == "antlr" {
						w.Walk(p.Tree)
						continue
					}
					tree, err := p.Native.Tree()
					if err != nil {
						b.Fatal(err)
					}
					New(w, nil).Walk(tree.Root())
				}
			}
		})
	}
}

// TestWalksAgree holds the native walk to ANTLR's: from the root of each GALA
// source staged as data (those TestEnginesAgree parses), and from each
// context the callers enter at (see entriesIn), both report the
// same references, at the same sites, with the same uses, in the same order,
// and call the hook alike.
func TestWalksAgree(t *testing.T) {
	paths := corpus(t)
	type source struct{ name, text string }
	var sources []source
	for i, in := range walkInputs {
		sources = append(sources, source{fmt.Sprintf("input %d", i), in})
	}
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source{path, string(text)})
	}

	walked := 0
	for _, src := range sources {
		p := parser.NewAntlrGalaParser().ParseFile(src.text)
		tree, _, err := p.Strict()
		if err != nil {
			continue // a source that does not parse is never walked
		}
		walked++
		root := tree.(antlr.ParserRuleContext)
		if msg := walkBoth(p, root); msg != "" {
			t.Errorf("%s: from the root: %s", src.name, msg)
			continue
		}
		for _, e := range entriesIn(root, nil) {
			if msg := walkBoth(p, e); msg != "" {
				t.Errorf("%s: from the %T at %d:%d: %s", src.name, e, e.GetStart().GetLine(), e.GetStart().GetColumn(), msg)
				break
			}
		}
	}
	// The repository's sources parse; a floor catches them not being staged.
	if walked < 200 {
		t.Fatalf("walked %d sources; the repository's are not staged", walked)
	}
}

// TestNewPairsTheHooks checks that New refuses an EnterFunctionScope option
// without its native twin, and a native twin without the option: the native
// walk never calls the option's hook, so an unpaired one would be dropped.
func TestNewPairsTheHooks(t *testing.T) {
	hook := func(*scopewalk.Walker, grammar.IFunctionDeclarationContext) {}
	enter := func(*scopewalk.Walker, native.FunctionDeclarationNode) {}
	for _, c := range []struct {
		name      string
		opts      scopewalk.Options
		enter     EnterFunctionScope
		wantPanic bool
	}{
		{"neither", scopewalk.Options{}, nil, false},
		{"both", scopewalk.Options{EnterFunctionScope: hook}, enter, false},
		{"option only", scopewalk.Options{EnterFunctionScope: hook}, nil, true},
		{"native only", scopewalk.Options{}, enter, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if got := recover() != nil; got != c.wantPanic {
					t.Fatalf("panicked = %v, want %v", got, c.wantPanic)
				}
			}()
			New(scopewalk.New(discard{}, c.opts), c.enter)
		})
	}
}
