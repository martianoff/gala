package nativewalk

import (
	"fmt"
	"strings"
	"testing"

	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/testcorpus"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// walkGolden holds TestWalkGolden's results: a line per source, its name, a
// digest of its text and a digest of what the walks report on it. It was
// written from the code on which the walk on the native tree reported, on
// every one of these sources, what the walk on ANTLR's tree reported
// (TestWalksAgree, run on the same sources).
const walkGolden = "internal/transpiler/scopewalk/nativewalk/testdata/walks.golden"

// TestWalkGolden walks each GALA source of the frozen corpus
// (internal/testcorpus), and walkInputs, from its root and from each node the
// walk's callers enter at (entriesOf), under every option set, and holds what
// the walks report (every reference with its site and use, and every call of
// the hook, in order) to its digest in walkGolden. Every source must have its
// line: a missing or stale line fails the test. Regenerate the golden with
// --test_env=GALA_UPDATE_WALK_GOLDEN set to its path in the source tree.
func TestWalkGolden(t *testing.T) {
	g := digestgolden.Open(t, walkGolden, "GALA_UPDATE_WALK_GOLDEN")
	type source struct{ name, text string }
	var sources []source
	for i, in := range walkInputs {
		sources = append(sources, source{fmt.Sprintf("input %d", i), in})
	}
	for _, name := range testcorpus.GalaNames() {
		sources = append(sources, source{name, testcorpus.Files()[name]})
	}
	walked := 0
	for _, src := range sources {
		nf := parser.NewAntlrGalaParser().ParseFile(src.text).CleanNative()
		if nf == nil {
			// A source that does not parse is never walked.
			g.Check(src.name, digestgolden.Digest(src.text), digestgolden.Digest("syntax error"), nil)
			continue
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
	if walked < 600 {
		t.Fatalf("walked %d sources of the corpus", walked)
	}
}

// walkAll is what the walks report on the tree of root, from root and from
// each entry of entriesOf, under every option set, with a line heading each
// walk.
func walkAll(root native.GalaNodeRef) []string {
	starts := append([]native.GalaNodeRef{root}, entriesOf(root)...)
	var rec recorder
	var out []string
	for i, set := range optionSets(&rec) {
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
