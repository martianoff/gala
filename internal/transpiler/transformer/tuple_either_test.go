package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTupleEither(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "Tuple extraction",
			input: `package main

func main() {
    val t = Tuple[int, string](V1 = 1, V2 = "hello")
    val res = t match {
        case Tuple(a, b) => a
        case _ => 0
    }
}`,
			expected: []string{
				"std.Tuple[int, string]",
				"std.UnapplyTuple(t)",
			},
		},
		{
			name: "Either extraction explicit type",
			input: `package main

func main() {
    val e Either[int, string] = Left[int, string](10)
    val res = e match {
        case Left(n) => n
        case Right(s) => len(s)
        case _ => 0
    }
}`,
			expected: []string{
				"std.Left_Apply",
				"std.UnapplyFull(e, std.Left{})",
				"std.UnapplyFull(e, std.Right{})",
			},
		},
		{
			name: "Either extraction implicit type",
			input: `package main

func main() {
    val e = Left[int, string](10)
    val res = e match {
        case Left(n) => n
        case Right(s) => len(s)
        case _ => 0
    }
}`,
			expected: []string{
				"std.Left_Apply",
				"std.UnapplyFull(e, std.Left{})",
				"std.UnapplyFull(e, std.Right{})",
			},
		},
		{
			name: "Either generic method",
			input: `package main

func main() {
    val e = Left[int, string](10)
    val mapped = e.Map[int]((s string) => len(s))
}`,
			expected: []string{
				"std.Either_Map[int](e",
			},
		},
		{
			name: "Either inferred type match",
			input: `package main

import "fmt"

func main() {
    val e1 = Left[int, string](10)
    val res = e1 match {
        case Left(n) => fmt.Sprintf("Left: %d", n)
        case Right(s) => fmt.Sprintf("Right: %s", s)
        case _ => "Unknown"
    }
}`,
			expected: []string{
				"std.Either[int, string]",
				"std.UnapplyFull(e1, std.Left{})",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			for _, exp := range tt.expected {
				assert.True(t, strings.Contains(got, exp), "Output missing %q\nGot:\n%s", exp, got)
			}
		})
	}
}
