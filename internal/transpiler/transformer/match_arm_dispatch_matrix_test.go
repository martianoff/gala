package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestMatchArmDispatchMatrix is the T4 fixture: it combines all three
// match-arm shapes that PR #214, #209, and #260 each had to fix
// independently — value arms, void-method arms, and panic-as-no-return
// arms — into a single match expression. The B8 unifying classifier
// (lowerMatchArmTailExpr) must now dispatch all three correctly.
//
// The match is intentionally used as a statement (not as a value), so
// any arm that produces a value or no-return is legitimate. The test
// asserts that the generated Go contains the expected dispatch shape:
//
//   - value arm  → bare expression (or assignment in a statement match)
//   - void arm   → bare statement, no `return …` wrap
//   - panic arm  → bare panic statement, no `return panic(…)`
//
// A regression in any of the three would either fail to compile (Go
// rejecting `return panic(...)` or `return <void-call>`) or produce
// generated Go where the panic is wrapped in a return statement.
func TestMatchArmDispatchMatrix(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	cases := []struct {
		name string
		// matchArms is the body inside `code match { ... }`. The match is
		// always wrapped as a statement-context expression (Println-receiver
		// or void).
		input string
		// notWrapped lists substrings that must NOT appear in the generated
		// Go — typically the broken `return panic(...)` form.
		notWrapped []string
		// contains lists substrings that MUST appear — the bare panic, the
		// void method call, etc.
		contains []string
	}{
		{
			// All three arm shapes in one statement match.
			name: "value+void+panic in single statement match",
			input: `package main

func handle(code Int) Unit = code match {
    case 0 => Println("zero")
    case 1 => Panic("one is forbidden")
    case _ => Println("other")
}

func main() {
    handle(0)
}`,
			notWrapped: []string{
				"return panic(",
			},
			contains: []string{
				"panic(\"one is forbidden\")",
			},
		},
		{
			// Same, but match is used as a value — exercises the
			// inferCommonResultType path with a NilType arm.
			name: "panic arm in value match returning Int",
			input: `package main

func resolve(opt Option[Int]) Int = opt match {
    case Some(n) => n
    case None()  => Panic("missing")
}

func main() {
    Println(resolve(Some(42)))
}`,
			notWrapped: []string{
				"return panic(",
			},
			contains: []string{
				"panic(\"missing\")",
			},
		},
		{
			// Default arm with panic — covers the second `lowerMatchArmTailExpr`
			// callsite in match.go (transformMatchClauses default-case path).
			name: "default arm panic",
			input: `package main

func describe(code Int) String = code match {
    case 0 => "zero"
    case _ => Panic("unknown")
}

func main() {
    Println(describe(0))
}`,
			notWrapped: []string{
				"return panic(",
			},
			contains: []string{
				"panic(\"unknown\")",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err, "transpile failed:\n%s", tc.input)
			for _, bad := range tc.notWrapped {
				require.False(t, strings.Contains(out, bad),
					"generated Go contains %q (expected bare panic, not wrapped)\n--- generated ---\n%s",
					bad, out)
			}
			for _, want := range tc.contains {
				require.True(t, strings.Contains(out, want),
					"generated Go missing %q\n--- generated ---\n%s",
					want, out)
			}
		})
	}
}
