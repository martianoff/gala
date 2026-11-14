package nativewalk

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// TestInterpolationParserPanicIsReported: an interpolated expression galop's
// parser panicked on (an internal compiler error, parser.IsParserPanic) is
// not walked, and the walk says so (Walker.Err), rather than leaving its
// references out unnoticed.
func TestInterpolationParserPanicIsReported(t *testing.T) {
	src := "package main\n\nfunc f(x int) string = s\"${x + 1}\"\n"
	tree, err := parser.NewAntlrGalaParser().ParseFile(src).CleanNative().Tree()
	if err != nil {
		t.Fatal(err)
	}
	walkIt := func() error {
		w := scopewalk.New(discard{}, scopewalk.Options{ParseInterpolations: true})
		New(w, nil).Walk(tree.Root())
		return w.Err()
	}
	if err := walkIt(); err != nil {
		t.Fatalf("a walk that parsed every part failed: %v", err)
	}
	panicked := galaerr.NewCodedSemanticError(galaerr.CodeInternalTransformerPanic, 1, 0,
		"internal compiler error: galop's parser panicked: injected", "")
	saved := parseExpressionTree
	defer func() { parseExpressionTree = saved }()
	parseExpressionTree = func(string) (native.ExpressionNode, []error) {
		return native.ExpressionNode{}, []error{panicked}
	}
	if err := walkIt(); !parser.IsParserPanic(err) {
		t.Fatalf("Err = %v, want the parser's internal error", err)
	}
}
