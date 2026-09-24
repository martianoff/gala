package transformer_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControlClauseCompositeLiteralParses pins the shapes that put a
// nullary sealed-type constructor — which lowers to the composite literal
// `Variant{}.Apply()` — into the header of a generated `if`/`for`. Go reads a
// bare `{` there as the start of the block body, so the literal has to be
// parenthesized or the emitted file does not parse.
func TestControlClauseCompositeLiteralParses(t *testing.T) {
	const sealedDecl = `package main

sealed type Dir {
	case LeftToRight()
	case RightToLeft()
}

`

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "if-expression condition",
			input: sealedDecl + `func flip(d Dir) string = if (d == RightToLeft()) "rtl" else "ltr"`,
		},
		{
			name: "if-statement condition",
			input: sealedDecl + `func flip(d Dir) string {
	if d == RightToLeft() { return "rtl" }
	return "ltr"
}`,
		},
		{
			name: "nested if-expression condition",
			input: sealedDecl + `func flip(d Dir) string =
	if (d == LeftToRight()) "ltr" else if (d == RightToLeft()) "rtl" else "?"`,
		},
		{
			name: "match arm guard",
			input: sealedDecl + `func flip(d Dir) string = d match {
	case other if other == RightToLeft() => "rtl"
	case _ => "ltr"
}`,
		},
		{
			name: "tail-recursive if-expression condition",
			input: sealedDecl + `func walk(d Dir, n int) int =
	if (d == RightToLeft()) walk(LeftToRight(), n+1) else n`,
		},
		{
			name: "for condition",
			input: sealedDecl + `func count(d Dir) int {
	var n = 0
	for d == RightToLeft() && n < 3 {
		n = n + 1
	}
	return n
}`,
		},
		{
			name: "for clause condition",
			input: sealedDecl + `func count(d Dir) int {
	var total = 0
	for i := 0; i < 3 && d == RightToLeft(); i = i + 1 {
		total = total + i
	}
	return total
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			tr := transformer.NewGalaASTTransformer()
			g := generator.NewGoCodeGenerator()
			trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

			got, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)

			_, parseErr := parser.ParseFile(token.NewFileSet(), "main.go", got, parser.AllErrors)
			require.NoError(t, parseErr, "generated Go must parse:\n%s", got)

			assert.NotContains(t, got, "RightToLeft{}.Apply()",
				"composite literal in a control clause must be parenthesized:\n%s", got)
		})
	}
}

// TestCompositeLiteralOutsideControlClauseUnchanged guards against the
// parenthesizing pass reaching expressions that are not in a control clause,
// where the extra parens would be noise.
func TestCompositeLiteralOutsideControlClauseUnchanged(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	got, err := trans.Transpile(`package main

sealed type Dir {
	case LeftToRight()
	case RightToLeft()
}

func useEq(d Dir) bool = d == RightToLeft()`, "")
	require.NoError(t, err)
	assert.True(t, strings.Contains(got, "RightToLeft{}.Apply()"),
		"expected an unparenthesized literal outside control clauses:\n%s", got)
}
