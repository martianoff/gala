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
func newThunkSugarTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return newCheckedTranspiler(p, a, tr, g)
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
	assert.Contains(t, body, "runTwice(existing)", "an existing thunk must be passed through unwrapped")
	assert.NotContains(t, body, "func() func() int", "must not double-wrap an existing thunk")
}

// A value of a function type as written is passed to a zero-argument
// function slot as itself. A value of a named function type fills it the one
// way Go accepts when only one is: as itself when its function type is the
// slot's, as the thunk's value when the slot's result holds it. When both
// are, or neither, or a slot's result is still a type parameter, it is the
// thunk's value.
func TestThunkSugar_FunctionValueSlots(t *testing.T) {
	trans := newThunkSugarTranspiler()
	const decls = `type Getter func() int

type Action func()

type AnyGetter func() any

func one() int = 1

func call(f func() int) int = f()

func run(f func()) {
    f()
}

func takeGetter(f func() Getter) int = f()()

func takeAny(f func() any) any = f()

func later[T any](f func() T) T = f()

func mkAction() Action = () => Println("act")

func mkFunc() func() = () => Println("act")

type Supplier func() Option[int]

func supply(f func() Option[int]) Option[int] = f()

struct Holder(g Getter, act Action)

type MakeHolder func() Holder

func build(f func() Holder) Holder = f()`
	cases := []struct {
		name     string
		main     string
		contains string
		absent   string
	}{
		{
			name:     "a named value for its own function type",
			main:     "val g Getter = one\n    Println(call(g))",
			contains: "call(g)",
			absent:   "func() int {",
		},
		{
			name:     "a named void value for a func() slot",
			main:     "val a Action = () => Println(1)\n    run(a)",
			contains: "run(a)",
			absent:   "run(func() {",
		},
		{
			name:     "a named value for a slot whose result is its type",
			main:     "val g Getter = one\n    Println(takeGetter(g))",
			contains: "takeGetter(func() Getter {",
		},
		{
			// T is inferred as int from g's function type, so the slot is a
			// func() int, and a thunk returning g as an int is no Go.
			name:     "a named value for a generic slot it binds",
			main:     "val g Getter = one\n    Println(later(g))",
			contains: "later(g)",
		},
		{
			name:     "a named value for Try",
			main:     "val g Getter = one\n    Println(Try(g).Get()())",
			contains: "func() Getter {",
		},
		{
			name:     "a call returning a named void function for a func() slot",
			main:     "run(mkAction())",
			contains: "run(func() {",
		},
		{
			name:     "a named value for a func() any slot",
			main:     "val h AnyGetter = () => 1\n    Println(takeAny(h))",
			contains: "takeAny(func() any {",
		},
		{
			name:     "a value as written for its own function type",
			main:     "val plain = () => 1\n    Println(call(plain))",
			contains: "call(plain)",
		},
		{
			name:     "a named value for its own function type with a generic sealed result",
			main:     "val s Supplier = () => Some(1)\n    Println(supply(s))",
			contains: "supply(s)",
			absent:   "supply(func()",
		},
		{
			name:     "a named value for its own function type with a struct result",
			main:     "val m MakeHolder = () => Holder(one, () => Println(1))\n    Println(build(m).g())",
			contains: "build(m)",
			absent:   "build(func()",
		},
		{
			name:     "a named field value for its own function type",
			main:     "val h = Holder(one, () => Println(1))\n    Println(call(h.g))",
			contains: "call(h.g.Get())",
		},
		{
			// A field read is a call of Get, which Go runs as a statement.
			name:     "a named void field value for a func() slot",
			main:     "val h = Holder(one, () => Println(1))\n    run(h.act)",
			contains: "run(func() {",
		},
		{
			name:     "a named value for a func() any slot as written",
			main:     "val g Getter = one\n    Println(takeAny(g))",
			contains: "takeAny(func() any {",
		},
		{
			name:     "a call returning a void function as written for a func() slot",
			main:     "run(mkFunc())",
			contains: "run(mkFunc())",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := "package main\n\n" + decls + "\n\nfunc main() {\n    " + tc.main + "\n}"
			out, err := trans.Transpile(input, "")
			require.NoError(t, err)
			body := mainBodyOf(t, out)
			assert.Contains(t, body, tc.contains, "generated:\n%s", out)
			if tc.absent != "" {
				assert.NotContains(t, body, tc.absent, "generated:\n%s", out)
			}
		})
	}
}

// An explicit lambda argument must keep working unchanged (the sugar only adds a
// path for bare expressions; lambdas already self-infer their return type).
func TestThunkSugar_ExplicitLambdaStillWorks(t *testing.T) {
	trans := newThunkSugarTranspiler()

	input := `package main

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
