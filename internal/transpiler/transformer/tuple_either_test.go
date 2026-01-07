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
	// Load base metadata so Tuple/Either are recognized
	searchPaths := []string{"../../.."}
	base := analyzer.GetBaseMetadata(p, searchPaths)
	a := analyzer.NewGalaAnalyzerWithBase(base, p, searchPaths)
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
			name: "Either extraction",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			assert.NoError(t, err)
			for _, exp := range tt.expected {
				assert.True(t, strings.Contains(got, exp), "Output missing %q\nGot:\n%s", exp, got)
			}
		})
	}
}
