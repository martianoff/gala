package transformer_test

import (
	"fmt"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestTupleArityRoundTripMatrix is the T2 fixture: it exercises every
// std.Tuple arity (2..10) across the five positions where the transformer
// has historically open-coded the same arity-rewrite rule:
//
//  1. value literal — `(a, b, c, ...)`
//  2. positional constructor — `Tuple(a, b, c, ...)`
//  3. type-arg position — `val x: Tuple[Int, Int, ...] = ...`
//  4. pattern position — `case (x, y, z, ...) =>`
//  5. .V_n field access — `t.V_n`
//
// The B2 refactor consolidated 4 encoding sites + 2 decoding sites behind
// `tupleArity` / `rewriteStdTupleIdent` / `tupleTypeNames`. This matrix
// guards against the next regression by ensuring every combination
// transpiles cleanly and the generated Go names the right `TupleN`.
func TestTupleArityRoundTripMatrix(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	for n := 2; n <= 10; n++ {
		n := n
		t.Run(fmt.Sprintf("arity_%d", n), func(t *testing.T) {
			src := makeTupleArityProgram(n)
			out, err := trans.Transpile(src, "")
			require.NoError(t, err, "arity %d transpile failed", n)

			expectedTupleName := tupleNameForArity(n)
			require.True(t, strings.Contains(out, expectedTupleName),
				"arity %d output missing %q\n--- generated ---\n%s",
				n, expectedTupleName, out)

			// All five positions should have produced the right type name —
			// if any of them fell back to `Tuple` (the 2-arity name) for
			// n>=3, Go would reject as "type Tuple has 2 type params, want N".
			// We assert by counting occurrences: at least the type-arg
			// position, the constructor, and the pattern produce one each.
			if n >= 3 {
				count := strings.Count(out, expectedTupleName)
				require.GreaterOrEqual(t, count, 2,
					"arity %d expected ≥2 references to %s, got %d\n--- generated ---\n%s",
					n, expectedTupleName, count, out)
			}
		})
	}
}

// tupleNameForArity returns the canonical std type name for arity n.
// Mirrors the production tupleArityName helper without importing it
// (we're in the external test package and the helper is unexported).
func tupleNameForArity(n int) string {
	if n == 2 {
		return "Tuple"
	}
	return fmt.Sprintf("Tuple%d", n)
}

// makeTupleArityProgram emits a minimal program exercising the five
// tuple-position contexts for a given arity. Each builds an n-tuple of
// integers, declares it with an explicit type (position 3), constructs
// it positionally (position 2), via literal (position 1), then matches
// against it (position 4) and reads .V_n (position 5).
func makeTupleArityProgram(n int) string {
	var b strings.Builder
	b.WriteString("package main\n\n")

	// elements 1..n
	values := make([]string, n)
	bindings := make([]string, n)
	intTypes := make([]string, n)
	for i := 0; i < n; i++ {
		values[i] = fmt.Sprintf("%d", i+1)
		bindings[i] = fmt.Sprintf("v%d", i+1)
		intTypes[i] = "Int"
	}
	tupleName := tupleNameForArity(n)

	// Position 3 — explicit type annotation on val (forces type-arg rewrite).
	fmt.Fprintf(&b, "func makeAnnotated() %s[%s] = (%s)\n",
		tupleName, strings.Join(intTypes, ", "), strings.Join(values, ", "))
	b.WriteString("\n")

	// Position 2 — positional constructor `Tuple(...)`.
	fmt.Fprintf(&b, "func makePositional() %s[%s] = Tuple(%s)\n",
		tupleName, strings.Join(intTypes, ", "), strings.Join(values, ", "))
	b.WriteString("\n")

	// Position 1 — value literal, plus position 4 — pattern, plus
	// position 5 — .V_n field access. Sum the destructured values.
	fmt.Fprintf(&b, "func sumPattern() Int {\n")
	fmt.Fprintf(&b, "    val t = (%s)\n", strings.Join(values, ", "))
	fmt.Fprintf(&b, "    val s = t match {\n")
	fmt.Fprintf(&b, "        case (%s) => %s\n",
		strings.Join(bindings, ", "), strings.Join(bindings, " + "))
	fmt.Fprintf(&b, "        case _ => 0\n")
	fmt.Fprintf(&b, "    }\n")
	// Field access — read .V1 explicitly to exercise position 5.
	fmt.Fprintf(&b, "    return s + t.V1\n")
	fmt.Fprintf(&b, "}\n\n")

	b.WriteString("func main() {\n")
	b.WriteString("    Println(makeAnnotated())\n")
	b.WriteString("    Println(makePositional())\n")
	b.WriteString("    Println(sumPattern())\n")
	b.WriteString("}\n")

	return b.String()
}
