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

// TestParseFileNative: a parse's native tree belongs to it alone; a node or
// a context of another parse has no counterpart, and no node has a context.
func TestParseFileNative(t *testing.T) {
	p := NewAntlrGalaParser().ParseFile("package main\n\nval x = 1\n")
	other := NewAntlrGalaParser().ParseFile("package other\n")
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
	p := NewAntlrGalaParser().ParseFile("package main\n\nfunc f(a int) int = a + 1\n")
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
