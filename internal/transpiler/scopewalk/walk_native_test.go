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
)

// recorder writes down everything a walk reports, in order: each reference
// with its site and use, and each call of the EnterFunctionScope hook.
type recorder struct{ events []string }

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
// when they agree). Each walk has its own recorder, so its own hook.
func walkBoth(p *parser.Parsed, ctx antlr.ParserRuleContext) string {
	var a, n recorder
	optsA, optsN := optionSets(&a), optionSets(&n)
	for i := range optsA {
		a.events, n.events = nil, nil
		wa := New(&a, optsA[i])
		wa.PushScope()
		antlrWalk{wa}.walk(ctx)

		wn := New(&n, optsN[i]).WithNative(p.Native)
		node, ok := wn.nativeNode(ctx)
		if !ok {
			return "the context has no native node"
		}
		wn.PushScope()
		nativeWalk{wn}.walk(node)

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

// lambdasIn is every lambda of tree, the contexts the capture analysis
// starts from.
func lambdasIn(tree antlr.Tree, out []antlr.ParserRuleContext) []antlr.ParserRuleContext {
	if l, ok := tree.(*grammar.LambdaExpressionContext); ok {
		out = append(out, l)
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		out = lambdasIn(tree.GetChild(i), out)
	}
	return out
}

// walkInputs exercise what positions are counted over (non-ASCII text before
// a name, CRLF line ends) and the walk's less common forms.
var walkInputs = []string{
	"package main\n\nfunc f(p int) string {\n    val s = \"héllo ✓ 日本\"; val t = s\"ü${p + q}ö$r\"\n    return s + t + u\n}\n",
	"package main\r\n\r\nfunc f(p int) int {\r\n    for k = range lookup(\":=\") { Println(k) }\r\n    for i, v := range xs { Println(i + v) }\r\n    return p match {\r\n        case Some(x: int) if x > lim => x\r\n        case Pair(a, b...) => a\r\n        case _ => g(name = p, h...)\r\n    }\r\n}\r\n",
	"package main\n\nfunc (b *Box[T]) m(x T = dflt) T = T{Field: x, Other: y}.v.w(z)\n\nval h = { case n => n + k }\n",
}

// TestWalksAgree holds the native walk to ANTLR's: from the root of every GALA
// source of the repository, and from each of its lambdas, both report the
// same references, at the same sites, with the same uses, in the same order,
// and call the hook alike.
func TestWalksAgree(t *testing.T) {
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
		for _, l := range lambdasIn(root, nil) {
			if msg := walkBoth(p, l); msg != "" {
				t.Errorf("%s: from the lambda at %d:%d: %s", src.name, l.GetStart().GetLine(), l.GetStart().GetColumn(), msg)
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
	src := "package main\n\nfunc f(p int) int {\n    val g = (q int) => q + p + outer\n    return g(1)\n}\n"
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
