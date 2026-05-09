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

// TestMatchReturnInsideStatementMatch verifies that a user-written
// `return X` inside a `match` arm at statement position (i.e. the
// match's value is discarded — it's used as a statement, not as the
// value of an expression) is lowered as a real Go return from the
// enclosing function, not as a return that only escapes the synthetic
// IIFE that lowers the match.
//
// The pre-fix behavior wrapped the match body in `func(obj T) { ... }(s)`
// and the user's `return X` was trapped inside that lambda — the
// surrounding gala function never returned, and any enclosing for-loop
// spun forever.
//
// The fix detects user-written returns in arm bodies (via
// stmtContainsUserReturn vs synthesizedReturns) and inlines the if-else
// chain directly into the enclosing block via pendingMatchStmtBlock.
func TestMatchReturnInsideStatementMatch(t *testing.T) {
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
			name: "return inside match arm at statement position",
			input: `package main

func firstPositive(opt Option[int]) int {
    opt match {
        case Some(x) => {
            if x > 0 {
                return x
            }
        }
        case None() => {}
    }
    return -1
}

func main() {
    Println(firstPositive(Some(7)))
}
`,
			// The match body is inlined; user's "return x" is a real Go
			// return from firstPositive, not trapped in an IIFE lambda.
			mustHave: []string{
				"return x",
			},
			mustMiss: []string{
				// The match must NOT be lowered as a value-returning IIFE
				// (which would trap `return x` in the lambda).
				"func(obj std.Option[int]) int",
				"func(obj std.Option[int]) string",
			},
		},
		{
			name: "return inside match arm nested in for loop — early exit",
			input: `package main

func firstOdd(xs Array[int]) int {
    var i = 0
    val n = xs.Size()
    for i < n {
        val v = xs.Get(i)
        Some(v) match {
            case Some(x) => {
                if x % 2 == 1 {
                    return x
                }
                i = i + 1
            }
            case None() => i = i + 1
        }
    }
    return -1
}
`,
			mustHave: []string{
				"return x",
				"return -1",
			},
			mustMiss: []string{
				// The outer match must NOT be lowered as a value-returning
				// IIFE that would swallow `return x`.
				"func(obj std.Option[int]) int {",
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
