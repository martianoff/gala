package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newThunkSugarTranspiler builds a transpiler wired with the std search path so
// companion-Apply resolution (e.g. `Box[T]`) works in these tests.
func newThunkSugarTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

func mainBodyOf(t *testing.T, out string) string {
	t.Helper()
	idx := strings.Index(out, "func main()")
	require.GreaterOrEqual(t, idx, 0)
	return out[idx:]
}

// By-name / thunk sugar: when a parameter's expected type is a zero-arg function
// type `func() T`, a plain expression argument is lifted into
// `func() T { return expr }`. This is the un-annotated generic companion-Apply
// case — `Box(compute())` must desugar to `Box(() => compute())` and infer T=int
// from the thunk's result.
func TestThunkSugar_UnannotatedGenericCompanionApply(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

import . "martianoff/gala/std"

sealed type Box[T any] {
    case box(value T)
}

func (b Box[T]) Apply(body func() T) Box[T] = box(value = body())
func (b Box[T]) Value() T = b.value

func compute() int = 7

func main() {
    val b = Box(compute())
    Println(b.Value())
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	body := mainBodyOf(t, out)

	// The bare expression must be lifted into a zero-arg thunk.
	assert.Contains(t, body, "func() int {", "expression must be wrapped in a func() int thunk")
	assert.Contains(t, body, "return compute()", "thunk body must return the original expression")
	// The thunk's result type must drive T=int inference and dispatch to Apply.
	assert.Contains(t, body, "Box[int]{}.Apply(", "must dispatch to companion Apply with T inferred to int")
}

// The concrete-annotation form `Box[int](compute())` must wrap identically,
// taking the result type from the explicit type argument.
func TestThunkSugar_AnnotatedGenericCompanionApply(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

import . "martianoff/gala/std"

sealed type Box[T any] {
    case box(value T)
}

func (b Box[T]) Apply(body func() T) Box[T] = box(value = body())
func (b Box[T]) Value() T = b.value

func compute() int = 7

func main() {
    val b = Box[int](compute())
    Println(b.Value())
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	body := mainBodyOf(t, out)

	assert.Contains(t, body, "func() int {", "expression must be wrapped in a func() int thunk")
	assert.Contains(t, body, "return compute()", "thunk body must return the original expression")
}

// A plain (non-generic) function whose parameter is `func() T` must also accept
// a bare expression, lifting it to a thunk.
func TestThunkSugar_NonGenericFuncParam(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

import . "martianoff/gala/std"

func runTwice(body func() int) int = body() + body()

func compute() int = 21

func main() {
    Println(runTwice(compute()))
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	body := mainBodyOf(t, out)

	assert.Contains(t, body, "func() int {", "expression must be wrapped in a func() int thunk")
	assert.Contains(t, body, "return compute()", "thunk body must return the original expression")
}

// An argument that is ALREADY a zero-arg function value must be passed through
// untouched — the sugar must never double-wrap it into `func() func() int`.
func TestThunkSugar_ExistingThunkPassThrough(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

import . "martianoff/gala/std"

func runTwice(body func() int) int = body() + body()

func main() {
    val existing = () => 21
    Println(runTwice(existing))
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	body := mainBodyOf(t, out)

	// existing is already func() int — it must be passed through as the thunk
	// value itself (here unwrapped from its Immutable val slot via .Get()),
	// never re-wrapped into `func() func() int`.
	assert.Contains(t, body, "runTwice(existing.Get())", "an existing thunk must be passed through unwrapped")
	assert.NotContains(t, body, "func() func() int", "must not double-wrap an existing thunk")
}

// An explicit lambda argument must keep working unchanged (the sugar only adds a
// path for bare expressions; lambdas already self-infer their return type).
func TestThunkSugar_ExplicitLambdaStillWorks(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

import . "martianoff/gala/std"

sealed type Box[T any] {
    case box(value T)
}

func (b Box[T]) Apply(body func() T) Box[T] = box(value = body())
func (b Box[T]) Value() T = b.value

func main() {
    val b = Box(() => 7)
    Println(b.Value())
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	body := mainBodyOf(t, out)

	assert.Contains(t, body, "Box[int]{}.Apply(", "explicit lambda must still infer T=int and dispatch to Apply")
}

// The std `Try` companion is the user-facing headline for the sugar: a bare
// single-expression argument (here a panic-capable GALA call) must be lifted
// into a `func() int` thunk so Try can run it lazily and catch a panic. This is
// the same companion-Apply shape as Box, exercised through the real std type.
// (The Go `(T, error)` multi-return case — e.g. `Try(strconv.Atoi(s))` — needs
// the Go SDK and is covered end-to-end by examples/expr_as_thunk_sugar.)
func TestThunkSugar_TrySingleExpression(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

import . "martianoff/gala/std"

func riskyDivide(a int, b int) int = a / b

func main() {
    val r = Try(riskyDivide(10, 2))
    Println(r.GetOrElse(-1))
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	body := mainBodyOf(t, out)

	assert.Contains(t, body, "func() int {", "bare Try argument must be wrapped in a func() int thunk")
	assert.Contains(t, body, "return riskyDivide(10, 2)", "thunk body must return the original expression")
	assert.Contains(t, body, "Try[int]{}.Apply(", "must dispatch to Try's companion Apply with T=int")
}
