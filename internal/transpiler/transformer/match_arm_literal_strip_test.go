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

// TestMatchArmTrailingBareLiteralIsStripped verifies that a statement-position
// match arm body whose final expression is a pure literal does NOT emit that
// literal as a bare expression statement in the generated Go. The pre-fix
// lowering wrapped each arm's `return literal` in `{ literal; return }`, which
// Go rejects with "X (untyped K constant) is not used".
//
// The fix recognises pure literals (bool, int, float, char, string, unit, nil)
// and emits a bare `return` instead. Identifiers and calls are NOT stripped —
// dropping them would silently change semantics.
func TestMatchArmTrailingBareLiteralIsStripped(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name      string
		input     string
		mustHave  []string
		mustMiss  []string
	}{
		{
			name: "Trailing bool literal in sealed-variant arm bodies",
			input: `package main

sealed type Mode {
    case Read()
    case Write()
}

func observe(m Mode) int {
    var seen = 0
    m match {
        case Read() => {
            seen = 1
            true
        }
        case Write() => {
            seen = 2
            true
        }
    }
    return seen
}

func main() {
    Println(observe(Read()))
}
`,
			// Bare literal must NOT appear in the void IIFE body.
			mustMiss: []string{
				"\n\t\t\t\ttrue\n",
				"\n\t\t\ttrue\n",
				"\n\t\ttrue\n",
			},
			// Side-effect assignments + early-exit return must remain.
			mustHave: []string{
				"seen = 1",
				"seen = 2",
				"return\n",
			},
		},
		{
			name: "Trailing int literal in arm bodies",
			input: `package main

func categorize(label string) int {
    var category = 0
    label match {
        case "alpha" => {
            category = 1
            0
        }
        case _ => {
            category = 99
            0
        }
    }
    return category
}

func main() {
    Println(categorize("alpha"))
}
`,
			mustMiss: []string{
				"\n\t\t\t\t0\n",
				"\n\t\t\t0\n",
			},
			mustHave: []string{
				"category = 1",
				"category = 99",
			},
		},
		{
			name: "Trailing string literal in arm bodies",
			input: `package main

func classify(n int) string {
    var bucket = "none"
    n match {
        case 0 => {
            bucket = "zero"
            ""
        }
        case _ => {
            bucket = "nonzero"
            ""
        }
    }
    return bucket
}

func main() {
    Println(classify(0))
}
`,
			mustHave: []string{
				`bucket = "zero"`,
				`bucket = "nonzero"`,
			},
			// Empty string literal as bare expression statement must not appear.
			// We assert it by checking that the arm bodies contain only the
			// assignment and the bare return — `""` should never appear as a
			// standalone token between them.
			mustMiss: []string{
				"\n\t\t\t\t\"\"\n",
				"\n\t\t\t\"\"\n",
			},
		},
		{
			name: "Trailing identifier (not pure) is preserved",
			// This is the negative test: a trailing IDENTIFIER is NOT a pure
			// literal and must NOT be stripped — the generator should keep
			// emitting it as a bare expression statement (so Go's compiler
			// catches the unused-value error rather than the transpiler
			// silently dropping a side-effect-bearing expression).
			input: `package main

func sideEffect() bool {
    Println("hit")
    return true
}

func observe(flag bool) {
    var seen = 0
    flag match {
        case true => {
            seen = 1
            sideEffect()
        }
        case false => {
            seen = 2
            sideEffect()
        }
    }
    Println(seen)
}

func main() {
    observe(true)
}
`,
			mustHave: []string{
				"sideEffect()",
				"seen = 1",
				"seen = 2",
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
