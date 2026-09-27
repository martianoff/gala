package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// An end-to-end pin on the arithmetic types that reach generated Go.
//
// It is deliberately not a regression test for the inference rule in
// arithmeticResultType: for these shapes the lambda's result type is settled by
// unification before that rule is consulted, and both assertions hold with the
// rule reverted. What it protects is the observable contract — a duration-valued
// lambda returns a Duration, an int-valued one an int — no matter which layer
// decides it, and that no future change to the rule pushes `any` into either.
func TestLambdaReturnTypeFollowsTheTypedOperand(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	src := `package main

import "time"

func Run(n int) {
    val timeout = () => 15 * time.Second
    val doubled = () => 2 * n
    Println(timeout(), doubled())
}
`
	out, err := trans.Transpile(src, "")
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if !strings.Contains(out, "func() time.Duration") {
		t.Errorf("`15 * time.Second` did not type as a Duration\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "func() int") {
		t.Errorf("`2 * n` did not type as an int\n--- got ---\n%s", out)
	}
}
