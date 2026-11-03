package nativewalk

import (
	"errors"
	"fmt"
	"testing"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// TestInterpolationConversionFailureIsReported: an interpolated expression
// galop's converter does not convert is not walked, and the walk says so
// (Walker.Err), rather than leaving its references out unnoticed.
func TestInterpolationConversionFailureIsReported(t *testing.T) {
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
		t.Fatalf("a walk that converted every part failed: %v", err)
	}
	saved := parseExpressionTree
	defer func() { parseExpressionTree = saved }()
	parseExpressionTree = func(string) (native.ExpressionNode, []error) {
		return native.ExpressionNode{}, []error{fmt.Errorf("%w: injected", parser.ErrConvertExpression)}
	}
	if err := walkIt(); !errors.Is(err, parser.ErrConvertExpression) {
		t.Fatalf("Err = %v, want the conversion failure", err)
	}
}
