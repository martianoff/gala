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
	trans := newCheckedTranspiler(p, a, tr, g)

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
		{
			// Negative: a tail-call argument that embeds a closure capturing a
			// parameter must NOT be rewritten. A loop would reassign `n` between
			// iterations and the escaped closure would observe the mutated value,
			// diverging from the per-frame binding of real recursion. It must fall
			// back to normal lowering (self-call preserved, no loop).
			name: "tail call with a closure-capturing argument falls back",
			input: `package main

func ident(f func() int) func() int = f

func loop(n int, acc func() int) func() int =
    if (n <= 0) acc else loop(n - 1, ident(() => n))

func main() {
    Println(loop(3, () => 0)())
}
`,
			mustMiss: []string{
				// The closure in the tail-call arg disqualifies TCO.
				"for {",
			},
		},
		{
			// A match applied to the self-call's result keeps the call out of
			// tail position: the self-call and its match survive, no loop.
			name: "self-call with a match on its result is not a tail call",
			input: `package main

func loop(n int, acc int) int = if (n <= 0) acc else loop(n - 1, acc + 1) match { case r => r * 2 }

func main() {
    Println(loop(3, 0))
}
`,
			mustHave: []string{
				"loop(n-1, acc+1)",
				"r * 2",
			},
			mustMiss: []string{
				"for {",
			},
		},
		{
			// A match applied to the whole if-expression (its else branch is a
			// block, so the match attaches to the if) puts the if out of tail
			// position: the match survives and nothing is rewritten.
			name: "if-expression with a match on its result is not rewritten",
			input: `package main

func f(n int) int = if (n > 0) f(n - 1) else { 1 } match { case r => r * 2 }

func main() {
    Println(f(3))
}
`,
			mustHave: []string{
				"return f(n - 1)",
				"r * 2",
			},
			mustMiss: []string{
				"for {",
			},
		},
		{
			// Guard: a method called on the self-call's result keeps the call
			// out of tail position too (callPattern already rejects the extra
			// suffixes; field access and a call on the result take the same
			// path).
			name: "self-call with a method on its result is not a tail call",
			input: `package main

func up(n int) Option[int] = if (n <= 0) Some(0) else up(n - 1).Map((x) => x + 1)

func main() {
    Println(up(3))
}
`,
			mustHave: []string{
				"up(n-1)",
			},
			mustMiss: []string{
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
