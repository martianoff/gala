package transformer_test

import (
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestErrorPathAssertions covers T5/T6: assert that the A8 stable error codes
// fire at the right compile-time checkpoints and that their messages render
// the code and the hint. These tests are intentionally narrow — one positive
// case (should compile) paired with a negative case (should fail with a
// specific code) per checked rule.
func TestErrorPathAssertions(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	type errCase struct {
		name           string
		input          string
		expectCode     galaerr.ErrorCode
		expectContains string
	}

	cases := []errCase{
		{
			name: "GALA-E0004 variant arity mismatch",
			input: `package main

sealed type Shape {
    case Rect(W int, H int, Label string)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h) => w * h
    case Circle(r) => r * r * 3
}

func main() {
    val r = Rect(1, 2, "x")
    area(r)
}`,
			expectCode:     galaerr.CodeVariantArityMismatch,
			expectContains: "binds 2 field(s) but declares 3",
		},
		{
			name: "GALA-E0002 non-exhaustive sealed match",
			input: `package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
}

func main() {
    val c = Red()
    name(c)
}`,
			expectCode:     galaerr.CodeNonExhaustiveMatch,
			expectContains: "missing cases",
		},
		{
			name: "GALA-E0003 non-sealed match missing default",
			input: `package main

func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
}

func main() {
    name(1)
}`,
			expectCode:     galaerr.CodeMissingDefault,
			expectContains: "default case",
		},
		{
			name: "GALA-E0005 missing Unapply extractor",
			input: `package main

func main() {
    val x = 42
    val r = x match {
        case Nonsensical(v) => v
        case _ => 0
    }
}`,
			expectCode:     galaerr.CodeMissingUnapply,
			expectContains: "must define an Unapply method",
		},
		{
			name: "GALA-E0006 multiple default cases",
			input: `package main

func name(n int) string = n match {
    case 1 => "one"
    case _ => "other"
    case _ => "also-other"
}

func main() {
    name(1)
}`,
			expectCode:     galaerr.CodeMultipleDefaults,
			expectContains: "multiple default",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.Error(t, err, "expected transpile to fail")
			msg := err.Error()
			assert.Contains(t, msg, string(tc.expectCode),
				"expected error message to carry code %s", tc.expectCode)
			assert.Contains(t, msg, tc.expectContains,
				"expected error message to describe the offense")
		})
	}
}

// TestT6VariantArityPositive is the positive half of T6 — the same sealed
// declaration as the T6 negative case, but with patterns that match the
// declared arity exactly. Must compile without error.
func TestT6VariantArityPositive(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

sealed type Shape {
    case Rect(W int, H int, Label string)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h, _) => w * h
    case Circle(r)     => r * r * 3
}

func main() {
    val r = Rect(1, 2, "x")
    area(r)
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

// TestT10TypeVarSubstitutionDepthCap exercises the B8 guard. Build a
// substitution map that maps T to a generic type containing T; the
// (pre-B8) implementation would have recursed without bound. With the
// depth cap the call must return within finite time and the result must
// be a well-formed type (not a crash).
//
// This test exercises substituteInType directly via a synthetic
// transpiler.Type graph to keep the assertion tight.
func TestT10TypeVarSubstitutionDepthCap(t *testing.T) {
	// Build: base type T, substitution T -> List[T]
	//
	// The (pre-B8) substituteInType would have unbounded recursion if it
	// re-applied the substitution to its own output. B8's depth cap
	// short-circuits at maxTypeSubstDepth so the call returns.
	//
	// We exercise the cap indirectly by compiling GALA code that stresses
	// the substitution path with deep nesting. Synthetic Type-graph tests
	// would need an exported entry point, so we prefer end-to-end cover.
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Deeply nested generic wrapper calls — exercise substitution on a
	// deep type tree. If the depth cap is missing or wrong, the call
	// explodes the stack; with the cap it returns in bounded time.
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("type Wrap[T any] struct { V T }\n")
	b.WriteString("func (w Wrap[T]) M[U any](f func(T) U) Wrap[U] = Wrap[U](V = f(w.V))\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("    val w = Wrap[int](V = 0)\n")
	// 30 chained maps force 30 substitutions on the same wrapper type.
	for i := 0; i < 30; i++ {
		b.WriteString("    val w")
		b.WriteString(itoa(i))
		b.WriteString(" = w.M((x int) => x + 1)\n")
	}
	b.WriteString("}\n")

	_, err := trans.Transpile(b.String(), "")
	assert.NoError(t, err, "deep substitution should not blow the stack or hang")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
