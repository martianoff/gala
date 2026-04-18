package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTranspiler is a tiny factory used by the coverage regressions below.
func newTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestT1PhantomTypeParamFallback pins the current behaviour of a generic
// function whose type parameter appears only in the return position.
//
// Today this falls back to `any` with a warnInference() message rather
// than a hard semantic error; see calls.go TODO(B5). The intent is that
// once every inference path threads the expected type, the fallback is
// promoted to a coded error. Until then, this test documents the
// behaviour so a regression into a panic or corrupted type is caught.
func TestT1PhantomTypeParamFallback(t *testing.T) {
	trans := newTranspiler()
	// `magic[T](x int) T` — T is only in the return position. Without
	// call-site context pinning T, the transpiler falls back to `any`.
	input := `package main

func magic[T](x int) T

func main() {
    val r = magic(42)
    _ = r
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err, "phantom type param should transpile (not panic)")
	assert.NotEmpty(t, out)
	// Document the current fallback: `any` shows up in the return position.
	// When B5 lands, replace this with an error-code assertion (the
	// surrounding commentary explains the promotion contract).
	assert.Contains(t, out, "any", "expected phantom T to fall back to `any` until B5 lands")
}

// TestT2UntypedLambdaFallback pins the current behaviour of a lambda
// whose parameter types cannot be inferred from the surrounding context.
//
// Today this falls back to `any` parameters with a warnInference()
// message; see declarations.go TODO(B11). The test guards against
// regression into a panic or corrupted Go output; once B11 lands, the
// assertion flips to an error-code check.
func TestT2UntypedLambdaFallback(t *testing.T) {
	trans := newTranspiler()
	// `val f = (x) => x + 1` — x has no annotation and no expected type.
	input := `package main

func main() {
    val f = (x) => x + 1
    _ = f
}`
	out, err := trans.Transpile(input, "")
	// Today: transpiles with `any` — acceptable until B11.
	// If this starts erroring, B11 has landed — upgrade the test to
	// assert the coded error and remove this branch.
	if err != nil {
		// B11 has landed; sanity-check that the error carries a code.
		assert.Contains(t, err.Error(), "GALA-E", "B11 fallback should emit a coded error")
		return
	}
	assert.NotEmpty(t, out)
	assert.Contains(t, out, "any", "expected untyped lambda param to fall back to `any` until B11 lands")
}

// TestT3ExhaustiveSealedNoDefaultEmitsUnreachable asserts that an
// exhaustive sealed match without an explicit `case _ =>` default
// compiles and emits a synthesized `panic("unreachable")` tail.
//
// This is the Phase 3 round-trip guarantee for sealed matches: if every
// variant is named, the generated Go still needs a syntactic default so
// Go's control flow is complete. The transpiler synthesises it; this
// test pins the generated shape.
func TestT3ExhaustiveSealedNoDefaultEmitsUnreachable(t *testing.T) {
	trans := newTranspiler()
	input := `package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func describe(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
    case Blue()  => "blue"
}

func main() {
    val c = Red()
    _ = describe(c)
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err, "exhaustive sealed match should compile without a default")
	assert.Contains(t, out, `panic("unreachable")`,
		"expected synthesised panic(\"unreachable\") default for exhaustive sealed match")
}

// TestT5ImmutableAutoUnwrapInInterpolation asserts that when an
// Immutable[T]-wrapped value (i.e. a `val`-bound binding) flows into a
// string interpolation, the transpiler unwraps it with .Get() so the
// interpolation reads the underlying value rather than the wrapper.
//
// Without the auto-unwrap, `s"hello $name"` would call String() on the
// Immutable wrapper (printing its address/struct form) instead of the
// intended string. The field-access auto-unwrap is already covered by
// examples/immutable_slice_field.gala; this test pins the interpolation
// site too.
func TestT5ImmutableAutoUnwrapInInterpolation(t *testing.T) {
	trans := newTranspiler()
	input := `package main

func main() {
    val name = "world"
    val s = s"hello $name"
    _ = s
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	// The generated Sprintf must read name.Get(), not name itself.
	assert.Contains(t, out, "name.Get()",
		"expected interpolation to auto-unwrap the Immutable val binding via .Get()")
	// And the Sprintf must not see the wrapper type leak as the argument.
	assert.NotContains(t, firstSprintf(out), "Immutable",
		"expected the interpolated argument to be unwrapped before Sprintf")
}

// TestT5ImmutableAutoUnwrapInTupleAccess asserts the same auto-unwrap
// contract for tuple element access: reading `.V1` / `.V2` on a
// `val`-bound tuple must unwrap the surrounding Immutable before
// projecting the field.
func TestT5ImmutableAutoUnwrapInTupleAccess(t *testing.T) {
	trans := newTranspiler()
	input := `package main

func main() {
    val pair = ("a", 42)
    val first = pair.V1
    _ = first
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	// The generated code should read pair.Get().V1, not pair.V1 directly.
	assert.Contains(t, out, "pair.Get().V1",
		"expected tuple access to auto-unwrap the Immutable val binding via .Get() before projecting V1")
}

// firstSprintf returns the first fmt.Sprintf(...) call from generated
// Go, or the full string if none found. Used by T5 to narrow the
// assertion window to the interpolation-generated call site.
func firstSprintf(out string) string {
	const needle = "fmt.Sprintf("
	i := strings.Index(out, needle)
	if i < 0 {
		return out
	}
	// Find the matching close paren naively — good enough for tests.
	depth := 0
	for j := i + len(needle) - 1; j < len(out); j++ {
		switch out[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return out[i : j+1]
			}
		}
	}
	return out[i:]
}
