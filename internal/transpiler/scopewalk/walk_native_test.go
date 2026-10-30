package scopewalk

// This file is built only with galop's parser (see BUILD.bazel): it walks the
// native tree.

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
)

// recorder writes down everything a walk reports, in order: each reference
// with its site and use, and each call of the EnterFunctionScope hook.
type recorder struct{ events []string }

var (
	_ Visitor = (*recorder)(nil)
	_ Visitor = discard{}
)

func (r *recorder) Reference(name string, at Site, use Use) {
	r.events = append(r.events, fmt.Sprintf("%s at %d:%d %q %+v", name, at.Line, at.Column, at.Text, use))
}

// optionSets are the option sets the walks are compared under, reporting to
// r: the capture analysis's, the undefined-symbol check's, and the blunt
// pattern binding. The hook records its call and binds the receiver's
// identifiers, as the analyzer's does, so a hook handed the wrong context
// changes what follows.
func optionSets(r *recorder) []Options {
	hook := func(w *Walker, fn grammar.IFunctionDeclarationContext) {
		start := fn.GetStart()
		r.events = append(r.events, fmt.Sprintf("enter %s at %d:%d", fn.Identifier().GetText(), start.GetLine(), start.GetColumn()))
		if recv := fn.Receiver(); recv != nil {
			w.BindIdentifiersIn(recv)
		}
	}
	return []Options{
		{ParseInterpolations: true},
		{
			ParseInterpolations:            true,
			RequireCleanInterpolationParse: true,
			SkipCompositeLiteralKeys:       true,
			SkipTypedPatternArgument:       true,
			EnterFunctionScope:             hook,
		},
		{BindWholePattern: true, EnterFunctionScope: hook},
	}
}

// walkBoth walks ctx, a context of p, on ANTLR's tree and on the native tree
// under every option set, and returns where the two reports first differ (""
// when they agree). A parameter list is walked as WalkParameterDefaults walks
// it, anything else as Walk does. Each walk has its own recorder, so its own
// hook.
func walkBoth(p *parser.Parsed, ctx antlr.ParserRuleContext) string {
	params, isParams := ctx.(*grammar.ParametersContext)
	var a, n recorder
	optsA, optsN := optionSets(&a), optionSets(&n)
	for i := range optsA {
		a.events, n.events = nil, nil
		wa := New(&a, optsA[i])
		wa.PushScope()
		wn := New(&n, optsN[i]).WithNative(p.Native)
		node, ok := wn.nativeNode(ctx)
		if !ok {
			return "the context has no native node"
		}
		wn.PushScope()
		if isParams {
			antlrWalk{wa}.walkParameterDefaults(params)
			nativeWalk{wn}.walkParameterDefaults(native.ParametersNode{GalaNodeRef: node})
		} else {
			antlrWalk{wa}.walk(ctx)
			nativeWalk{wn}.walk(node)
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

func (discard) Reference(string, Site, Use) {}

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
	opts := Options{ParseInterpolations: true, RequireCleanInterpolationParse: true, SkipCompositeLiteralKeys: true, SkipTypedPatternArgument: true}
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
					w := New(discard{}, opts)
					if mode != "antlr" {
						w.WithNative(p.Native)
					}
					w.Walk(p.Tree)
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

// TestEntryPointsWalkNative: an entry point handed a context of the Walker's
// parse walks its native node, and one handed any other context walks ANTLR's
// tree, with the same result.
func TestEntryPointsWalkNative(t *testing.T) {
	src := `package main

func f(p int) int {
    val g = (q int) => q + p + outer
    return g(1)
}
`
	p := parser.NewAntlrGalaParser().ParseFile(src)
	other := parser.NewAntlrGalaParser().ParseFile(src)
	tree, _, err := p.Strict()
	if err != nil {
		t.Fatal(err)
	}
	var mine, theirs recorder
	w := New(&mine, Options{}).WithNative(p.Native)
	if _, ok := w.nativeNode(tree); !ok {
		t.Fatal("the Walker's own root has no native node")
	}
	otherTree, _, _ := other.Strict()
	if _, ok := w.nativeNode(otherTree); ok {
		t.Fatal("another parse's root has a native node")
	}
	w.Walk(tree)
	New(&theirs, Options{}).WithNative(p.Native).Walk(otherTree)
	if got, want := strings.Join(mine.events, "\n"), strings.Join(theirs.events, "\n"); got != want || got == "" {
		t.Fatalf("the native walk reports\n%s\nANTLR's\n%s", got, want)
	}
}
