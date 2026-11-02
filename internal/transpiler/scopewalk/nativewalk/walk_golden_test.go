package nativewalk

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// walkGolden holds TestWalkGolden's results: a line per source, its name, a
// digest of its text and a digest of what the walks report on it. It was
// written from the code on which the walk on the native tree reported, on
// every one of these sources, what the walk on ANTLR's tree reported.
const walkGolden = "internal/transpiler/scopewalk/nativewalk/testdata/walks.golden"

// TestWalkGolden walks each GALA source staged as data, and walkInputs, from
// its root and from each node the walk's callers enter at (entriesOf), under
// every option set, and holds what the walks report (every reference with
// its site and use, and every call of the hook, in order) to its digest in
// walkGolden. Refresh the golden with --test_env=GALA_UPDATE_WALK_GOLDEN set
// to its path in the source tree.
func TestWalkGolden(t *testing.T) {
	g := digestgolden.Open(t, walkGolden, "GALA_UPDATE_WALK_GOLDEN")
	type source struct{ name, text string }
	var sources []source
	for i, in := range walkInputs {
		sources = append(sources, source{fmt.Sprintf("input %d", i), in})
	}
	files, err := bazel.ListRunfiles()
	if err != nil {
		t.Fatalf("cannot list the runfiles: %v", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ShortPath < files[j].ShortPath })
	for _, f := range files {
		if !strings.HasSuffix(f.ShortPath, ".gala") {
			continue
		}
		text, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source{f.ShortPath, string(text)})
	}
	walked := 0
	for _, src := range sources {
		nf := parser.NewAntlrGalaParser().ParseFile(src.text).CleanNative()
		if nf == nil {
			continue // a source that does not parse is never walked
		}
		tree, err := nf.Tree()
		if err != nil {
			t.Fatalf("%s: %v", src.name, err)
		}
		walked++
		events := walkAll(tree.Root())
		g.Check(src.name, digestgolden.Digest(src.text), digestgolden.Digest(strings.Join(events, "\n")), func() string {
			return strings.Join(events, "\n")
		})
	}
	g.Finish("TestWalkGolden: per source, a digest of its text and of what the walks report on it.")
	// The repository's sources parse; floors catch them not being staged
	// and the golden going stale.
	if walked < 200 {
		t.Fatalf("walked %d sources; the repository's are not staged", walked)
	}
	if !g.Updating() && g.Compared < walked*9/10 {
		t.Fatalf("%d of %d sources held to the golden: refresh it", g.Compared, walked)
	}
}

// walkAll is what the walks report on the tree of root, from root and from
// each entry of entriesOf, under every option set, with a line heading each
// walk.
func walkAll(root native.GalaNodeRef) []string {
	starts := append([]native.GalaNodeRef{root}, entriesOf(root)...)
	var rec, unused recorder
	var out []string
	for i, set := range optionSets(&unused, &rec) {
		for _, n := range starts {
			rec.events = nil
			w := scopewalk.New(&rec, set.opts)
			nw := New(w, set.enter)
			w.PushScope()
			if n.Kind() == native.GalaKIND_parameters {
				nw.WalkParameterDefaults(native.ParametersNode{GalaNodeRef: n})
			} else {
				nw.Walk(n)
			}
			start := n.Start()
			out = append(out, fmt.Sprintf("== options %d from %s at %d:%d", i, native.GalaRuleNames[n.Rule()], start.Line, start.Column))
			out = append(out, rec.events...)
		}
	}
	return out
}

// entriesOf is every node under root the walk's callers start from, in
// source order: the lambdas (the capture analysis), and the expression lists
// and parameter lists (the undefined-symbol check's top-level vals and
// struct shorthands).
func entriesOf(root native.GalaNodeRef) []native.GalaNodeRef {
	var out []native.GalaNodeRef
	root.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_lambdaExpression, native.GalaKIND_expressionList, native.GalaKIND_parameters:
			if n != root {
				out = append(out, n)
			}
		}
		return true
	})
	return out
}
