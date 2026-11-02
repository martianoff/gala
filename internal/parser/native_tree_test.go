package parser

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

// dumpNative prints everything a native tree holds: its nodes, children,
// token labels, error children, conjured tokens, tokens and hidden tokens,
// and the text they index.
func dumpNative(t *native.GalaTree) string {
	return fmt.Sprintf("nodes %v\nkids %v\nlabels %v\nerrors %v\nconjured %v %q\ntokens %v\nhidden %v\nsrc %q",
		t.Nodes, t.Kids, t.TokenLabels, t.ErrorKids, t.Conjured, t.ConjuredText, t.Tokens, t.Hidden, t.Src)
}

// antlrReference is src parsed by ANTLR's parser (error-recovered when src
// has syntax errors), its tree converted to the native form, and the ANTLR
// context of each native node, by node index: what galop's native parse of
// src is held to.
type antlrReference struct {
	text string // treeText of ANTLR's tree
	tree *native.GalaTree
	ctxs []antlr.ParserRuleContext
	// nodes maps a context to its node's index.
	nodes map[antlr.ParserRuleContext]int
}

func referenceOf(src string) (antlrReference, error) {
	src = galaerr.StripBOM(src)
	r := parseSourceFile(src)
	root, ok := r.tree.(antlr.ParserRuleContext)
	if !ok {
		return antlrReference{}, fmt.Errorf("ANTLR's parse gave no tree")
	}
	tree, _, ctxs := grammar.GalaToNativeContexts(root, r.stream, src)
	if tree == nil {
		return antlrReference{}, fmt.Errorf("the converter gave no tree")
	}
	ref := antlrReference{text: treeText(r.tree), tree: tree, ctxs: ctxs, nodes: map[antlr.ParserRuleContext]int{}}
	for i, c := range ctxs {
		ref.nodes[c] = i
	}
	return ref, nil
}

// checkNative checks p's native tree, a parse of src: it is the native tree
// of ANTLR's parse of src, converted (dumpNative prints both the same), and
// prints as ANTLR's tree; every node has a context of its rule in ANTLR's
// tree, starting at the same token; and the native helpers agree with the
// ANTLR ones on every node (checkNativeHelpers). TestEnginesAgree runs it on
// the parses it makes.
func checkNative(p *Parsed, src string) string {
	n := p.RecoveredNative()
	tree, err := n.Tree()
	if err != nil {
		return err.Error()
	}
	if again, _ := n.Tree(); again != tree {
		return "a second Tree call converted again"
	}
	ref, err := referenceOf(src)
	if err != nil {
		return err.Error()
	}
	if want, got := dumpNative(ref.tree), dumpNative(tree); got != want {
		return "the native tree is not ANTLR's tree converted: " + diffAt(want, got)
	}
	if got := tree.Root().ToStringTree(); got != ref.text {
		return "the native tree prints differently from the ANTLR tree: " + diffAt(ref.text, got)
	}
	if len(ref.ctxs) != len(tree.Nodes) {
		return fmt.Sprintf("%d contexts for %d nodes", len(ref.ctxs), len(tree.Nodes))
	}
	nodeOf := func(ctx antlr.ParserRuleContext) native.GalaNodeRef {
		if i, ok := ref.nodes[ctx]; ok {
			return tree.Node(i)
		}
		return native.GalaNodeRef{}
	}
	for i := range tree.Nodes {
		node := tree.Node(i)
		ctx := ref.ctxs[i]
		switch {
		case ctx == nil:
			return fmt.Sprintf("node %d has no context", i)
		case ctx.GetRuleIndex() != node.Rule():
			return fmt.Sprintf("node %d is rule %d, its context rule %d", i, node.Rule(), ctx.GetRuleIndex())
		}
		if msg := checkNativeHelpers(nodeOf, ctx, node); msg != "" {
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

// parseConverted parses src with ANTLR's engine, whatever the build's
// default, so that its native tree is ANTLR's tree converted, error-recovered
// when src has errors (see RecoveredNative).
func parseConverted(src string) *Parsed {
	var p *Parsed
	withEngine(EngineANTLR, func() { p = NewAntlrGalaParser().ParseFile(src) })
	return p
}

// TestNativeForCleanParses: a parse without errors has a native tree with
// either engine; galop's has no ANTLR tree, ANTLR's engine's has one. A
// parse with syntax errors, by either engine, has ANTLR's error-recovered
// tree and no clean native tree, only the recovered one.
func TestNativeForCleanParses(t *testing.T) {
	const clean, broken = "package main\n\nval x = 1\n", "package main\n\nval x = \n"
	galop := parseWithGalop(clean)
	if galop.Native == nil || !galop.Native.Converted() {
		t.Error("galop's parse has no native tree of its own")
	}
	if galop.Tree != nil {
		t.Error("galop's clean parse has an ANTLR tree")
	}
	antlrClean, antlrBroken := parseConverted(clean), parseConverted(broken)
	if antlrClean.Tree == nil || antlrClean.Native == nil || antlrClean.Native.Converted() {
		t.Error("the clean parse with ANTLR's engine has no ANTLR tree, or no native tree to convert")
	}
	if _, ok := antlrClean.Native.Root(); !ok {
		t.Error("the clean parse with ANTLR's engine does not convert")
	}
	for name, p := range map[string]*Parsed{"ANTLR's engine": antlrBroken, "galop's engine": parseWithGalop(broken)} {
		if len(p.Errors) == 0 {
			t.Fatalf("%s: the broken source parses", name)
		}
		if p.Native != nil || p.CleanNative() != nil {
			t.Errorf("%s: the parse of a syntax error has a clean native tree", name)
		}
		if p.Tree == nil || p.RecoveredNative() == nil {
			t.Errorf("%s: the parse of a syntax error has no recovered tree", name)
		}
	}
}

// TestParseFileNative: a parse's native tree belongs to it alone.
func TestParseFileNative(t *testing.T) {
	const src = "package main\n\nval x = 1\n"
	p := parseWithGalop(src)
	other := parseWithGalop("package other\n")
	if msg := checkNative(p, src); msg != "" {
		t.Fatal(msg)
	}
	tree, _ := p.Native.Tree()
	otherTree, err := other.Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if tree == otherTree {
		t.Error("two parses share a native tree")
	}
}

// TestParseFileNativeConcurrent asks for one parse's native tree from many
// goroutines, for galop's parse and for one to convert: they get the same
// one (run under the race detector in CI).
func TestParseFileNativeConcurrent(t *testing.T) {
	const src = "package main\n\nfunc f(a int) int = a + 1\n"
	for name, p := range map[string]*Parsed{"galop": parseWithGalop(src), "ANTLR": parseConverted(src)} {
		got := make([]*native.GalaTree, 16)
		var wg sync.WaitGroup
		for i := range got {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got[i], _ = p.Native.Tree()
				p.Native.Root()
			}()
		}
		wg.Wait()
		for i := range got {
			if got[i] == nil || got[i] != got[0] {
				t.Fatalf("%s: goroutine %d got %p, goroutine 0 %p", name, i, got[i], got[0])
			}
		}
	}
}

// TestNativeParsePanicFallsBack makes galop's native parser panic: the parse
// must still succeed, on ANTLR's parser, with its tree converted.
func TestNativeParsePanicFallsBack(t *testing.T) {
	savedFile, savedExpr := nativeParseSourceFile, nativeParseExpression
	defer func() { nativeParseSourceFile, nativeParseExpression = savedFile, savedExpr }()
	nativeParseSourceFile = func(*native.GalaTokens) (native.SourceFileNode, error) { panic("injected") }
	nativeParseExpression = func(*native.GalaTokens) (native.ExpressionNode, error) { panic("injected") }
	before := galopPanics.Load()

	const src = "package main\n\nval x = 1\n"
	p := parseWithGalop(src)
	if len(p.Errors) > 0 || p.Tree == nil {
		t.Fatalf("the fallback parse failed: errors %v, tree %v", p.Errors, p.Tree)
	}
	if msg := checkNative(p, src); msg != "" {
		t.Error(msg)
	}

	// The expression entry points fall back to ANTLR's parser, converted.
	want, wantTree := parseExpressionAtWith(EngineANTLR, "a + b", 3, 4), parseExpressionWith(EngineANTLR, "a + b")
	if got := parseExpressionAtWith(EngineGalop, "a + b", 3, 4); got != want || strings.Contains(got, "error:") {
		t.Errorf("ParseExpressionCheckedAt after a panic: %s", diffAt(want, got))
	}
	if gotTree := parseExpressionWith(EngineGalop, "a + b"); gotTree != wantTree {
		t.Errorf("ParseExpressionTree after a panic: %s", diffAt(wantTree, gotTree))
	}
	if n := galopPanics.Load() - before; n != 3 {
		t.Errorf("%d galop panics counted, want 3", n)
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
		res := parseSourceFile(input)
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
