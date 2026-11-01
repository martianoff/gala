package parser

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

// checkNative checks p's native tree: it prints as the ANTLR tree, whose text
// is text (treeText); the converter gives one context per node, the root's
// last; and the bridge pairs every node with a context of its rule, starting
// at the same token, both ways; and the native helpers agree with the ANTLR
// ones on every node (checkNativeHelpers). TestEnginesAgree runs it on the
// parses it makes.
func checkNative(p *Parsed, text string) string {
	n := p.Native
	tree, err := n.Tree()
	if err != nil {
		return err.Error()
	}
	if again, _ := n.Tree(); again != tree {
		return "a second Tree call converted again"
	}
	if got := tree.Root().ToStringTree(); got != text {
		return "the native tree prints differently from the ANTLR tree: " + diffAt(text, got)
	}
	if len(n.ctxs) != len(tree.Nodes) {
		return fmt.Sprintf("%d contexts for %d nodes", len(n.ctxs), len(tree.Nodes))
	}
	if last := len(n.ctxs) - 1; tree.Root().Index() != last || n.ctxs[last] != p.Tree {
		return "the root, and its context the tree, are not last"
	}
	if !n.Bridged() {
		return "the bridge does not hold"
	}
	for i := range tree.Nodes {
		node := tree.Node(i)
		ctx := n.Ctx(node)
		switch {
		case ctx == nil:
			return fmt.Sprintf("node %d has no context", i)
		case ctx.GetRuleIndex() != node.Rule():
			return fmt.Sprintf("node %d is rule %d, its context rule %d", i, node.Rule(), ctx.GetRuleIndex())
		case n.Node(ctx) != node:
			return fmt.Sprintf("node %d's context maps to node %d", i, n.Node(ctx).Index())
		}
		if msg := checkNativeHelpers(n, ctx, node); msg != "" {
			return fmt.Sprintf("node %d (%s): %s", i, native.GalaRuleNames[node.Rule()], msg)
		}
		// A node that spans no token has no first token to compare.
		if node.StopIndex() >= node.StartIndex() {
			if got, want := NativeDocKeyOf(node), DocKeyOf(ctx.GetStart()); got != want {
				return fmt.Sprintf("node %d starts at %d:%d, its context at %d:%d", i, got.Line, got.Column, want.Line, want.Column)
			}
		}
	}
	return ""
}

// parseWithGalop parses src as a source file with galop's engine, whatever
// the build's default.
func parseWithGalop(src string) *Parsed {
	var p *Parsed
	withEngine(EngineGalop, func() { p = NewAntlrGalaParser().ParseFile(src) })
	return p
}

// parseConverted parses src with ANTLR's parser and gives the parse a native
// tree all the same, error-recovered when src has syntax errors: ParseFile
// gives such a parse none, but galop's converter takes it, so the native
// helpers are checked on error-recovered trees too.
func parseConverted(src string) *Parsed {
	r := parseSourceFile(src, EngineANTLR)
	return &Parsed{Tree: r.tree, Errors: r.errors, Native: newNative(r.tree, r.stream, src)}
}

// TestNativeForCleanParses: a parse without syntax errors has a native tree,
// whichever parser made its tree: galop's, or ANTLR's with ANTLR's engine,
// whose tree converts and bridges as galop's does. A parse with syntax
// errors, by either engine, has none, so its consumers take the ANTLR paths.
func TestNativeForCleanParses(t *testing.T) {
	const clean, broken = "package main\n\nval x = 1\n", "package main\n\nval x = \n"
	if parseWithGalop(clean).Native == nil {
		t.Error("galop's parse has no native tree")
	}
	var antlrClean, antlrBroken *Parsed
	withEngine(EngineANTLR, func() {
		antlrClean = NewAntlrGalaParser().ParseFile(clean)
		antlrBroken = NewAntlrGalaParser().ParseFile(broken)
	})
	if !antlrClean.Native.Bridged() {
		t.Error("the clean parse with ANTLR's engine has no native tree that bridges")
	}
	if antlrBroken.Native != nil {
		t.Error("the parse of a syntax error with ANTLR's engine has a native tree")
	}
	p := parseWithGalop(broken)
	if len(p.Errors) == 0 {
		t.Fatal("the broken source parses")
	}
	if p.Native != nil {
		t.Error("ANTLR's fallback parse of a syntax error has a native tree")
	}
}

// TestParseFileNative: a parse's native tree belongs to it alone; a node or
// a context of another parse has no counterpart, and no node has a context.
func TestParseFileNative(t *testing.T) {
	p := parseWithGalop("package main\n\nval x = 1\n")
	other := parseWithGalop("package other\n")
	if msg := checkNative(p, treeText(p.Tree)); msg != "" {
		t.Fatal(msg)
	}
	otherTree, err := other.Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if p.Native.Ctx(otherTree.Root()) != nil {
		t.Error("a node of another parse has a context")
	}
	if p.Native.Node(other.Tree.(antlr.ParserRuleContext)).Ok() {
		t.Error("a context of another parse has a node")
	}
	if p.Native.Ctx(native.GalaNodeRef{}) != nil {
		t.Error("no node has a context")
	}
}

// TestParseFileNativeConcurrent asks for one parse's native tree from many
// goroutines: they get the same one (run under the race detector in CI).
func TestParseFileNativeConcurrent(t *testing.T) {
	p := parseWithGalop("package main\n\nfunc f(a int) int = a + 1\n")
	got := make([]*native.GalaTree, 16)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = p.Native.Tree()
			p.Native.Node(p.Tree.(antlr.ParserRuleContext))
		}()
	}
	wg.Wait()
	for i := range got {
		if got[i] == nil || got[i] != got[0] {
			t.Fatalf("goroutine %d got %p, goroutine 0 %p", i, got[i], got[0])
		}
	}
}

// TestBridgeHolds: the bridge holds for a converted parse, and not once any
// node lacks its context or has another rule's, a context is missing, or the
// tree has no nodes. The source has every kind of node the transformer's
// top-level walk reads on the native tree (types, signatures,
// parameters, blocks, expressions, sealed and opaque declarations).
func TestBridgeHolds(t *testing.T) {
	p := parseWithGalop(`package main

type P struct {
    var a int
    b []string
}

type I interface {
    M(x int) string
}

sealed type S {
    case A(v int)
    case B()
}

opaque type O int

struct Q(x int, y string)

val x, y = 1, 2

func (p P) f(n int) int { return n }

func g() int = 2
`)
	if len(p.Errors) > 0 {
		t.Fatal(p.Errors)
	}
	tree, err := p.Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if !p.Native.Bridged() {
		t.Fatal("a converted parse is not bridged")
	}
	for i := range tree.Nodes {
		// No context, then another rule's: the root's, or for the root the
		// last node's before it.
		other := p.Native.ctxs[tree.Root().Index()]
		if i == tree.Root().Index() {
			other = p.Native.ctxs[i-1]
		}
		for _, ctx := range []antlr.ParserRuleContext{nil, other} {
			ctxs := append([]antlr.ParserRuleContext(nil), p.Native.ctxs...)
			ctxs[i] = ctx
			if bridgeHolds(tree, ctxs) {
				t.Errorf("bridged with context %T for node %d (%s)", ctx, i, native.GalaRuleNames[tree.Node(i).Rule()])
			}
		}
	}
	if bridgeHolds(tree, p.Native.ctxs[1:]) {
		t.Error("bridged with a context missing")
	}
	if bridgeHolds(&native.GalaTree{}, nil) {
		t.Error("an empty tree is bridged")
	}
}

// TestCleanNative: a parse has a native tree only when it has no errors.
// Both parsers accept each source below, but a check that runs after the
// parse rejects it, so the parse gets no native tree and its readers take
// the ANTLR tree, as they do on a syntax error.
func TestCleanNative(t *testing.T) {
	clean := parseWithGalop("package main\n\nval x = 1\n")
	if clean.CleanNative() == nil || clean.CleanNative() != clean.Native {
		t.Fatal("a clean parse gives no handle")
	}
	// A parse whose tree has no syntax errors but which ParseFile rejects
	// all the same has no native tree either, with either engine: Native is
	// set only when the parse reports no error at all.
	for name, src := range map[string]string{
		"illegal character":                      "package main\n\nval s = \"a\uFEFFb\"\n",
		"no blank line after the package clause": "package main\nval x = 1\n",
	} {
		for _, engine := range []Engine{EngineGalop, EngineANTLR} {
			var p *Parsed
			withEngine(engine, func() { p = NewAntlrGalaParser().ParseFile(src) })
			if len(p.Errors) == 0 {
				t.Fatalf("%s (%s): ParseFile reports no error", name, engine)
			}
			if p.Native != nil || p.CleanNative() != nil {
				t.Errorf("%s (%s): a parse with an error has a native tree", name, engine)
			}
		}
	}
}

// brokenInputs are syntax errors ANTLR's recovery handles in different ways:
// a missing token it conjures, extra tokens it skips, an unclosed block, a
// truncated file.
var brokenInputs = []string{
	"package main\n\nfunc f( {\n}\n",
	"package main\n\nfunc main() {\n    val x = (1 + \n}\n",
	"package main\n\nfunc main() {\n    Println(\"a\"\n}\n",
	"package main\n\nval x = 1 2 3\n",
	"package main\n\nfunc main() {\n",
	"package main\n\ntype T struct {\n    a int\n",
	"package main\n\nfunc f() = x match {\n    case => 1\n}\n",
	"package main\n\nval = \n",
	"package\n",
	"",
	"package main\n\n)))\n",
	"package main\n\nfunc main() {\n    if (x { y }\n}\n",
	"package main\n\nsealed type S {\n    case A(\n}\n",
	"package main\n\nimport\n\nval x = 1\n",
}

// errorNodes is the texts of the error nodes under tree, in order.
func errorNodes(tree antlr.Tree, out []string) []string {
	if n, ok := tree.(antlr.ErrorNode); ok {
		return append(out, n.GetText())
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		out = errorNodes(tree.GetChild(i), out)
	}
	return out
}

// nativeErrorChildren is errorNodes for a native tree: the texts of its error
// children.
func nativeErrorChildren(n native.GalaNodeRef, out []string) []string {
	for i := range n.ChildCount() {
		c := n.Child(i)
		switch {
		case c.IsError():
			out = append(out, n.Tree().Text(c.Terminal().Index()))
		case !c.IsToken():
			out = nativeErrorChildren(c.Node(), out)
		}
	}
	return out
}

// TestConverterOnRecoveredTrees converts ANTLR's error-recovered trees to the
// native form: the converter must not panic, the native tree must print as
// the ANTLR tree, and its error children must be the ANTLR tree's error nodes,
// in order. The inputs are brokenInputs and broken variants of the corpus.
func TestConverterOnRecoveredTrees(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	inputs := slices.Clone(brokenInputs)
	for n, src := range sourceTexts(t) {
		if n%4 == 0 {
			inputs = append(inputs, breakSource(r, src), breakSource(r, src))
		}
	}
	recovered, withErrorNodes := 0, 0
	for k, input := range inputs {
		res := parseSourceFile(input, EngineANTLR)
		if len(res.errors) == 0 {
			continue
		}
		recovered++
		root, ok := res.tree.(antlr.ParserRuleContext)
		if !ok {
			t.Errorf("input %d (%.60q): no recovered tree", k, input)
			continue
		}
		var tree *native.GalaTree
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("input %d (%.60q): the converter panicked: %v", k, input, p)
				}
			}()
			tree, _, _ = grammar.GalaToNativeContexts(root, res.stream, input)
		}()
		if tree == nil {
			t.Errorf("input %d (%.60q): the converter gave no tree", k, input)
			continue
		}
		if w, g := treeText(res.tree), tree.Root().ToStringTree(); w != g {
			t.Errorf("input %d (%.60q): the native tree prints differently: %s", k, input, diffAt(w, g))
		}
		want := errorNodes(res.tree, nil)
		if got := nativeErrorChildren(tree.Root(), nil); !slices.Equal(want, got) {
			t.Errorf("input %d (%.60q): error nodes %q, native error children %q", k, input, want, got)
		}
		if len(want) > 0 {
			withErrorNodes++
		}
	}
	if withErrorNodes < 10 {
		t.Errorf("only %d of %d recovered trees have error nodes", withErrorNodes, recovered)
	}
	t.Logf("%d recovered trees, %d with error nodes", recovered, withErrorNodes)
}
