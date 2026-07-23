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

// TestSelfTailCallOptimization verifies that direct self-tail-recursion in an
// expression-bodied function whose body is an if-expression is rewritten into a
// `for {}` loop with a simultaneous parameter reassignment, and that non-tail
// recursion is left as an ordinary self-call.
func TestSelfTailCallOptimization(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		mustHave []string
		mustMiss []string
	}{
		{
			// Positive: the tail self-call becomes a loop; the else branch does
			// NOT contain a self-call to factorial( anymore.
			name: "factorial tail recursion becomes a loop",
			input: `package main

func factorial(n int, acc int) int = if (n <= 0) acc else factorial(n - 1, n * acc)

func main() {
    Println(factorial(10, 1))
}
`,
			mustHave: []string{
				"for {",
				// Simultaneous reassignment of both params from temporaries
				// computed with the OLD values.
				"n, acc = ",
				"return acc",
			},
			mustMiss: []string{
				// The recursive self-call must be gone from the loop body.
				// (`factorial(n-1` only appears in the rewritten self-call; the
				// declaration is `func factorial(n int...` and main calls
				// `factorial(10, 1)`.)
				"factorial(n-1",
				"return factorial(",
			},
		},
		{
			// Negative: non-tail recursion (n * factorial(n-1)) must be left
			// unchanged — no loop, the self-call survives.
			name: "non-tail recursion is left unchanged",
			input: `package main

func factorial(n int) int = if (n <= 0) 1 else n * factorial(n - 1)

func main() {
    Println(factorial(5))
}
`,
			mustHave: []string{
				// The genuine recursive call is preserved.
				"factorial(n-1)",
			},
			mustMiss: []string{
				// No TCO loop was emitted for the recursive function.
				"for {",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation should succeed")
			gen := stripGeneratedHeader(got)
			for _, frag := range tt.mustHave {
				assert.True(t, strings.Contains(gen, frag),
					"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
			}
			for _, frag := range tt.mustMiss {
				assert.False(t, strings.Contains(gen, frag),
					"expected generated code to NOT contain %q\n--- generated ---\n%s", frag, gen)
			}
		})
	}
}
