package parser

import (
	"fmt"
	"sync"
	"testing"

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
// the build's default: only galop's parses have a native tree.
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

// TestNativeOnlyForGalopParses: a tree galop's parser made has a native tree;
// one ANTLR's parser made, with ANTLR's engine or as galop's fallback on a
// syntax error, has none, so its consumers take the ANTLR paths.
func TestNativeOnlyForGalopParses(t *testing.T) {
	const clean, broken = "package main\n\nval x = 1\n", "package main\n\nval x = \n"
	if parseWithGalop(clean).Native == nil {
		t.Error("galop's parse has no native tree")
	}
	var antlrClean, antlrBroken *Parsed
	withEngine(EngineANTLR, func() {
		antlrClean = NewAntlrGalaParser().ParseFile(clean)
		antlrBroken = NewAntlrGalaParser().ParseFile(broken)
	})
	if antlrClean.Native != nil || antlrBroken.Native != nil {
		t.Error("a parse with ANTLR's engine has a native tree")
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
