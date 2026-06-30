package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"
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
// The transpiler no longer erases an uninferable T to `any`; it emits a
// concrete Go generic (`func magic[T any]() std.Option[T]`) and leaves
// inference to Go at the call site. This test guards against a regression
// into a panic, corrupted output, or a silent `any` erasure.
func TestT1PhantomTypeParamFallback(t *testing.T) {
	trans := newTranspiler()
	// `magic[T any]() Option[T]` — T is only in the return position.
	input := `package main

func magic[T any]() Option[T] = None[T]()

func main() {
    val r = magic()
    _ = r
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err, "phantom type param should transpile (not panic)")
	assert.NotEmpty(t, out)
	// The return-only T is preserved as a real Go type parameter rather than
	// erased to `any`.
	assert.Contains(t, out, "func magic[T any]()", "expected phantom T to stay a concrete generic param")
}

// TestT2UntypedLambdaFallback pins the behaviour of a lambda whose parameter
// types cannot be inferred from the surrounding context.
//
// A bare lambda initializer with no declared type has no expected type to draw
// from, so its parameter would have to be emitted as `any`. Rather than emit
// non-concrete Go, the transpiler rejects it with GALA-E0033 and a remediation
// hint. (Typed contexts — a function-typed val, function argument, or return —
// thread their declared signature into the lambda; see
// TestUntypedLambdaTypedContextThreads.)
func TestT2UntypedLambdaFallback(t *testing.T) {
	trans := newTranspiler()
	// `val f = (x) => x + 1` — x has no annotation and no expected type.
	input := `package main

func main() {
    val f = (x) => x + 1
    _ = f
}`
	_, err := trans.Transpile(input, "")
	require.Error(t, err, "expected an untyped lambda parameter to be rejected")
	assert.Contains(t, err.Error(), string(galaerr.CodeUntypedLambdaParam),
		"expected the coded GALA-E0033 error")
	assert.Contains(t, err.Error(), `lambda parameter "x" has no type`,
		"expected the error to name the offending parameter")
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

// TestIfExpressionStringInterpolationIIFEReturnType pins the regression
// where an if-expression whose arms were s-string interpolations lowered
// to `func() any { ... }()` instead of `func() string { ... }()`.
//
// The bug: the per-branch type oracle used by transformIfExpression's
// IIFE-typing fallback could not always reach inside the lowered
// `fmt.Sprintf(...)` CallExpr to recover its `string` return type
// (e.g. when the Go SDK was unavailable for type info, falling back
// to the universal `any` widening). The fix tags every Sprintf call
// produced by buildSprintfCall with type `string` so the per-branch
// oracle resolves it without re-deriving the return type from `fmt`.
//
// Verifies the IIFE wrapper for an if-expression whose arms are s-string
// interpolations is `func() string`, not `func() any`.
func TestIfExpressionStringInterpolationIIFEReturnType(t *testing.T) {
	trans := newTranspiler()
	input := `package main

func makeLabel(use bool, name string) string {
    val label = if (use) s">> $name" else s"-- $name"
    return label
}

func main() {
    _ = makeLabel(true, "alice")
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	// The if-expression IIFE wrapper must be typed as `func() string`,
	// not `func() any` — otherwise downstream consumers expecting a
	// concrete `string` (e.g. `NewImmutable[string]`) would reject the
	// `any` value with "need type assertion".
	assert.Contains(t, out, "func() string {",
		"expected if-expression IIFE wrapper to be func() string for s-string interpolation arms")
	assert.NotContains(t, out, "func() any {",
		"if-expression IIFE wrapper must not widen to func() any when both arms are string-interpolation s-strings")
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
