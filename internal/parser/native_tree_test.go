package parser

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// dumpNative prints everything a native tree holds: its nodes, children,
// token labels, error children, conjured tokens, tokens and hidden tokens,
// and the text they index.
func dumpNative(t *native.GalaTree) string {
	return fmt.Sprintf("nodes %v\nkids %v\nlabels %v\nerrors %v\nconjured %v %q\ntokens %v\nhidden %v\nsrc %q",
		t.Nodes, t.Kids, t.TokenLabels, t.ErrorKids, t.Conjured, t.ConjuredText, t.Tokens, t.Hidden, t.Src)
}

// TestNilNative: a nil *Native, what every parse without a native tree
// carries, reports that it has none, without panicking.
func TestNilNative(t *testing.T) {
	var n *Native
	if tree, err := n.Tree(); tree != nil || err == nil {
		t.Errorf("Tree() = %v, %v; want no tree and an error", tree, err)
	}
	if root, ok := n.Root(); ok || root.Ok() {
		t.Error("Root() found a node")
	}
	if n.TreeOnly() != nil {
		t.Error("TreeOnly() gave a handle")
	}
}

// TestNativeForCleanParses: a parse without errors has a native tree; a parse
// with syntax errors has no clean native tree, only the recovered one.
func TestNativeForCleanParses(t *testing.T) {
	const clean, broken = "package main\n\nval x = 1\n", "package main\n\nval x = \n"
	if _, ok := parseGalop(t, clean).Native.Root(); !ok {
		t.Error("the clean parse has no native tree")
	}
	p := parseGalop(t, broken)
	if len(p.Errors) == 0 {
		t.Fatal("the broken source parses")
	}
	if p.Native != nil || p.CleanNative() != nil {
		t.Error("the parse of a syntax error has a clean native tree")
	}
	if _, ok := p.RecoveredNative().Root(); !ok {
		t.Error("the parse of a syntax error has no recovered tree")
	}
}

// TestParseFileNative: a parse's native tree belongs to it alone.
func TestParseFileNative(t *testing.T) {
	tree, err := parseGalop(t, "package main\n\nval x = 1\n").Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	otherTree, err := parseGalop(t, "package other\n").Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if tree == otherTree {
		t.Error("two parses share a native tree")
	}
}

// TestParseFileNativeConcurrent asks for one parse's native tree from many
// goroutines: they get the same one (run under the race detector in CI).
func TestParseFileNativeConcurrent(t *testing.T) {
	p := parseGalop(t, "package main\n\nfunc f(a int) int = a + 1\n")
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
			t.Fatalf("goroutine %d got %p, goroutine 0 %p", i, got[i], got[0])
		}
	}
}

// TestTreeOnlyTrims: a parse leaves its native tree's slices as the parser
// sized them, and TreeOnly, which a holder that keeps the handle calls, cuts
// the spare capacity off them and keeps the tree. The comments make the token
// slice, which galop's lexer sizes by the length of the text, mostly spare.
func TestTreeOnlyTrims(t *testing.T) {
	src := "package main\n\n" + strings.Repeat("// a comment line, which holds no default-channel token\n", 40) +
		"func f(a int) int = a + 1\n"
	// untrimmed names tree's slices of which more than an eighth is spare.
	untrimmed := func(tree *native.GalaTree) []string {
		var out []string
		for name, s := range map[string][2]int{
			"nodes":  {len(tree.Nodes), cap(tree.Nodes)},
			"kids":   {len(tree.Kids), cap(tree.Kids)},
			"tokens": {len(tree.Tokens), cap(tree.Tokens)},
			"hidden": {len(tree.Hidden), cap(tree.Hidden)},
			"labels": {len(tree.TokenLabels), cap(tree.TokenLabels)},
		} {
			if s[1]-s[0] > s[0]/8 {
				out = append(out, name)
			}
		}
		slices.Sort(out)
		return out
	}
	p := parseGalop(t, src)
	tree, err := p.Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	dump := dumpNative(tree)
	if before := untrimmed(tree); !slices.Contains(before, "tokens") {
		t.Fatalf("the parse's token slice has little spare (untrimmed: %v), so the test checks nothing", before)
	}
	if p.Native.TreeOnly() != p.Native {
		t.Error("TreeOnly gives another handle")
	}
	after, _ := p.Native.Tree()
	if after != tree || dumpNative(after) != dump {
		t.Error("TreeOnly changed the tree")
	}
	if spare := untrimmed(after); len(spare) > 0 {
		t.Errorf("after TreeOnly, %v keep more than an eighth spare", spare)
	}
}

// internalError is err as the internal compiler error of a galop panic, at
// line:col, or why it is not one.
func internalError(err error, line, col int) (*galaerr.SemanticError, string) {
	var se *galaerr.SemanticError
	switch {
	case !errors.As(err, &se):
		return nil, fmt.Sprintf("%v is not a semantic error", err)
	case !IsParserPanic(err) || se.Code != galaerr.CodeInternalTransformerPanic:
		return nil, fmt.Sprintf("%v is not an internal error", err)
	case se.Line != line || se.Column != col:
		return nil, fmt.Sprintf("%v is at %d:%d, want %d:%d", err, se.Line, se.Column, line, col)
	case !strings.Contains(se.Msg, "internal compiler error: galop's parser panicked: injected") || se.Hint == "":
		return nil, fmt.Sprintf("%v (hint %q) does not say galop's parser panicked", err, se.Hint)
	}
	return se, ""
}

// injectParserPanics makes galop's parsers panic, as a galop bug would, until
// t ends.
func injectParserPanics(t *testing.T) {
	savedFile, savedExpr := parseSourceFile, parseExpression
	parseSourceFile = func(*native.GalaTokens) (native.SourceFileNode, []native.GalaParseError) { panic("injected") }
	parseExpression = func(*native.GalaTokens) (native.ExpressionNode, []native.GalaParseError) { panic("injected") }
	t.Cleanup(func() { parseSourceFile, parseExpression = savedFile, savedExpr })
}

// TestGalopPanicIsParserPanic makes galop's parser panic: the panic must
// not escape, and the parse reports it as an internal compiler error
// (GALA-E0017) for the file, at 1:0, with no tree; an expression's parse
// reports it at the expression's position. A source-text error is still
// reported first. GALA_PANIC_STACK=1 prints the panic and its stack.
func TestGalopPanicIsParserPanic(t *testing.T) {
	injectParserPanics(t)
	p := NewAntlrGalaParser().ParseFile("package main\n\nval x = 1\n")
	if len(p.Errors) != 1 {
		t.Fatalf("errors %v, want the internal error alone", p.Errors)
	}
	if _, msg := internalError(p.Errors[0], 1, 0); msg != "" {
		t.Error(msg)
	}
	if p.Native != nil || p.RecoveredNative() != nil {
		t.Error("the parse that panicked has a tree")
	}

	p = NewAntlrGalaParser().ParseFile("package main\n\nval s = \"a\x00b\"\n")
	if len(p.Errors) != 2 || IsParserPanic(p.Errors[0]) {
		t.Fatalf("errors %v, want the source-text error, then the internal error", p.Errors)
	}
	if _, msg := internalError(p.Errors[1], 1, 0); msg != "" {
		t.Error(msg)
	}

	if expr, err := ParseExpressionCheckedAt("a + b", 3, 4, "test expression"); expr.Ok() {
		t.Error("ParseExpressionCheckedAt: the parse that panicked has a tree")
	} else if _, msg := internalError(err, 3, 4); msg != "" {
		t.Errorf("ParseExpressionCheckedAt: %s", msg)
	}
	expr, errs := ParseExpressionTree("a + b")
	if expr.Ok() || len(errs) != 1 {
		t.Fatalf("ParseExpressionTree: tree %v, errors %v; want no tree and the internal error", expr.Ok(), errs)
	}
	if _, msg := internalError(errs[0], 1, 0); msg != "" {
		t.Errorf("ParseExpressionTree: %s", msg)
	}

	// The stack goes to standard error under GALA_PANIC_STACK=1.
	t.Setenv("GALA_PANIC_STACK", "1")
	stderr := captureStderr(t, func() {
		NewAntlrGalaParser().ParseFile("package main\n")
	})
	if !strings.Contains(stderr, "galop's parser panicked") || !strings.Contains(stderr, "goroutine") {
		t.Errorf("GALA_PANIC_STACK=1 printed %q, want the panic and its stack", stderr)
	}
}

// captureStderr runs f and returns what it wrote to standard error.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stderr = saved }()
	f()
	w.Close()
	return <-done
}

// TestCleanNative: a parse has a native tree only when it has no errors.
// The parser accepts each source below, but a check that runs after the
// parse rejects it, so the parse gets no clean native tree.
func TestCleanNative(t *testing.T) {
	clean := parseGalop(t, "package main\n\nval x = 1\n")
	if clean.CleanNative() == nil || clean.CleanNative() != clean.Native {
		t.Fatal("a clean parse gives no handle")
	}
	for name, src := range map[string]string{
		"illegal character":                      "package main\n\nval s = \"a\x00b\"\n",
		"no blank line after the package clause": "package main\nval x = 1\n",
	} {
		p := parseGalop(t, src)
		if len(p.Errors) == 0 {
			t.Fatalf("%s: ParseFile reports no error", name)
		}
		if p.Native != nil || p.CleanNative() != nil {
			t.Errorf("%s: a parse with an error has a native tree", name)
		}
		if _, ok := p.RecoveredNative().Root(); !ok {
			t.Errorf("%s: no recovered tree", name)
		}
	}
}

// brokenInputs are syntax errors the recovery handles in different ways: a
// missing token it conjures, extra tokens it skips, an unclosed block, a
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

// nativeErrorChildren is the texts of the error children under n, in order.
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

// TestRecoveredTrees parses brokenInputs and broken variants of the staged
// sources: every parse with errors must have an error-recovered tree, and
// the recovery must mark skipped tokens as error children in some of them.
// The parser golden holds the recovered trees of the frozen corpus.
func TestRecoveredTrees(t *testing.T) {
	inputs := slices.Clone(brokenInputs)
	for _, src := range sourceTexts(t) {
		inputs = append(inputs, fileVariants(src)[1:3]...)
	}
	recovered, withErrorChildren := 0, 0
	for k, input := range inputs {
		p := parseGalop(t, input)
		if len(p.Errors) == 0 {
			continue
		}
		for _, err := range p.Errors {
			if IsParserPanic(err) {
				t.Errorf("input %d (%.60q): %v", k, input, err)
			}
		}
		recovered++
		tree, err := p.RecoveredNative().Tree()
		if err != nil {
			t.Errorf("input %d (%.60q): no recovered tree: %v", k, input, err)
			continue
		}
		if len(nativeErrorChildren(tree.Root(), nil)) > 0 {
			withErrorChildren++
		}
	}
	if withErrorChildren < 10 {
		t.Errorf("only %d of %d recovered trees have error children", withErrorChildren, recovered)
	}
	t.Logf("%d recovered trees, %d with error children", recovered, withErrorChildren)
}
