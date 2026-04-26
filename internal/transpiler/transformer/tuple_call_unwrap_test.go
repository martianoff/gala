package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

// TestTupleCallFieldUnwrap covers auto-unwrap of Tuple's V_N field after a
// CallExpr-rooted access. The companion case for the val-bound shape lives
// in tuple_field_unwrap_repro_test.go; this one specifically targets the
// `funcCall().V_N` shape that previously left std.Immutable[T] in
// arithmetic context.
func TestTupleCallFieldUnwrap(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "func returning Tuple — V1 in arithmetic",
			input: `package main

import "fmt"

func termSize() Tuple[int, int] = (80, 24)

func main() {
    fmt.Println(termSize().V1 + 1)
}
`,
			want: "termSize().V1.Get()",
		},
		{
			name: "func returning Tuple — V2 used as int return",
			input: `package main

import "fmt"

func termSize() Tuple[int, int] = (80, 24)

val overhead = 14

func availHeight() int = termSize().V2 - overhead

func main() {
    fmt.Println(availHeight())
}
`,
			want: "termSize().V2.Get()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err)
			assert.True(t, strings.Contains(got, tt.want),
				"expected %q in generated Go, got:\n%s", tt.want, got)
		})
	}
}
