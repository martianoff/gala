package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"os"
	"path/filepath"
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
// function slot as itself. So is a value of a named function type whose
// function type is the slot's, however it is reached — a variable, a field,
// a call, a conversion. Where the slot's function type is another — its
// result is the named type, or a type parameter not yet inferred — the value
// is the thunk's result.
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
			contains: "run(mkAction())",
			absent:   "run(func() {",
		},
		{
			name:     "a named value for a func() any slot of its own function type",
			main:     "val h AnyGetter = () => 1\n    Println(takeAny(h))",
			contains: "takeAny(h)",
			absent:   "takeAny(func() any {",
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
			name:     "a named void field value for a func() slot",
			main:     "val h = Holder(one, () => Println(1))\n    run(h.act)",
			contains: "run(h.act.Get())",
			absent:   "run(func() {",
		},
		{
			name:     "a conversion to a named void function type for a func() slot",
			main:     "run(Action(() => Println(1)))",
			contains: "run(Action(",
			absent:   "run(func() {",
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

// Inside a generic declaration whose type parameter is named like Try's, a
// named function value passed to Try is still the thunk's result: Try's T is
// not yet inferred, whatever the enclosing T is called.
func TestThunkSugar_NamedFunctionValueTryInGenericBody(t *testing.T) {
	trans := newThunkSugarTranspiler()
	input := `package main

type Getter func() int

func one() int = 1

func tryIn[T any](g Getter, x T) Try[Getter] = Try(g)

func main() {
    val g Getter = one
    Println(tryIn(g, 1).Get()())
}`
	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	assert.Contains(t, out, "func() Getter {", "generated:\n%s", out)
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

// By-name arguments to a method with type parameters of its own, lowered to
// a free function (#1302): a plain expression is lifted to its thunk as it
// is for a generic function, the thunk's result binding the method's U; a
// generic function of no parameters is a thunk already, passed as it is,
// and one with parameters is the thunk's value; a thunk lifted for a
// Sendable parameter is checked as a lambda there is (GALA-E0037).
func TestThunkSugar_GenericMethodByName(t *testing.T) {
	const decls = `package main

struct Box[T any](V T)

func (b Box[T]) Lazy[U any](d func() U, on bool) Option[U] = if (on) Some(d()) else None()

func (b Box[T]) Seeded[U any](seed U, d func() U) U = d()

func (b Box[T]) Run[U any](f Sendable[func() U]) U = f()

func (b Box[T]) Fn(d func() func(int) int) int = d()(4)

func (b Box[T]) Lazy2[U any, V any](d func() U, v V) U = d()

func side() int = 1

func id[X any](x X) X = x

func zero[X any]() X {
    var z X
    z
}

`
	tests := []struct {
		name        string
		main        string
		contains    []string
		notContains []string
		wantErr     string
	}{
		{
			name:        "positional, named and explicit type argument",
			main:        "val b = Box(1)\n    Println(b.Lazy(side(), true))\n    Println(b.Lazy(on = false, d = side()))\n    Println(b.Lazy[int](side(), true))",
			contains:    []string{"Box_Lazy(b, func() int {", "Box_Lazy[int, int](b, func() int {"},
			notContains: []string{"(b, side()"},
		},
		{
			name:        "generic function reference passed as itself",
			main:        "val b = Box(1)\n    Println(b.Seeded(1, zero))",
			contains:    []string{"Box_Seeded(b, 1, zero"},
			notContains: []string{"return zero"},
		},
		{
			name:     "generic function reference with parameters is the thunk's value",
			main:     "val b = Box(1)\n    Println(b.Fn(id))",
			contains: []string{"return id"},
		},
		{
			name:     "generic function reference with parameters in a bound slot",
			main:     "val b = Box(1)\n    Println(b.Lazy2[func(int) int](id, 1)(4))",
			contains: []string{"func() func(int) int {"},
		},
		{
			name:     "Sendable parameter over a val",
			main:     "val n = 1\n    val b = Box(1)\n    Println(b.Run(n + 1))",
			contains: []string{"return n + 1"},
		},
		{
			name:    "Sendable parameter over a var",
			main:    "var n = 1\n    val b = Box(1)\n    Println(b.Run(n + 1))",
			wantErr: "GALA-E0037",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newThunkSugarTranspiler().Transpile(decls+"func main() {\n    "+tt.main+"\n}", "")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			body := mainBodyOf(t, out)
			for _, want := range tt.contains {
				assert.Contains(t, body, want)
			}
			for _, unwanted := range tt.notContains {
				assert.NotContains(t, body, unwanted)
			}
		})
	}
}

// A by-name argument whose type is a type parameter of the enclosing
// declaration is lifted to a thunk of that type, which the generated Go names
// (#1370), for a generic function and a method with type parameters of its own.
func TestThunkSugar_EnclosingTypeParam(t *testing.T) {
	input := `package main

struct Box[T any](V T)

func (b Box[T]) Lazy[U any](d func() U) U = d()

func lazy[U any](d func() U) U = d()

func viaFunction[T any](x T) T = lazy(x)

func viaMethod[T any](x T, b Box[int]) T = b.Lazy(x)

func main() {
    Println(viaFunction(5), viaMethod("s", Box(1)))
}`

	out, err := newThunkSugarTranspiler().Transpile(input, "")
	require.NoError(t, err)
	for _, want := range []string{"return lazy(func() T {", "return Box_Lazy(b, func() T {"} {
		assert.Contains(t, out, want)
	}
}

// TestThunkSugar_NilForAFunctionParam pins #1448: `nil` for a zero-arg
// function parameter whose thunk could not return nil (`func() state`, a
// struct result, or a void `func()`) is the function itself, passed as is.
// For a result nil can be (`func() *state`, `func() any`), the thunk
// returning nil stays, as before. #1466 adds complex numbers, Go named arrays,
// Go aliases (followed to their target, a pointer one keeping the thunk), an
// anonymous Go `struct{}` and a parenthesized `(nil)`, with the Go types in a
// bridge.go file of the package.
func TestThunkSugar_NilForAFunctionParam(t *testing.T) {
	cases := []struct {
		name, param, want, notWant string
	}{
		{"a struct result", "func() state", "b(nil, 2)", "return nil"},
		{"an int result", "func() int", "b(nil, 2)", "return nil"},
		{"no result", "func()", "b(nil, 2)", "func() {"},
		{"a string result", "func() string", "b(nil, 2)", "return nil"},
		{"an opaque result", "func() Count", "b(nil, 2)", "return nil"},
		{"a sealed result", "func() Shape", "b(nil, 2)", "return nil"},
		{"a fieldless struct result", "func() Empty", "b(nil, 2)", "return nil"},
		{"a generic struct result", "func() Option[int]", "b(nil, 2)", "return nil"},
		{"a Go struct result", "func() time.Time", "b(nil, 2)", "return nil"},
		{"a pointer result", "func() *state", "return nil", ""},
		{"an any result", "func() any", "return nil", ""},
		{"an error result", "func() error", "return nil", ""},
		{"a slice result", "func() []int", "return nil", ""},
		{"a map result", "func() map[string]int", "return nil", ""},
		{"a GALA interface result", "func() Shower", "return nil", ""},
		{"a Go named map result", "func() http.Header", "return nil", ""},
		{"a Go interface result", "func() io.Reader", "return nil", ""},
		{"a function result", "func() func() int", "return nil", ""},
		// #1466: more types nil cannot be, from the package's .go file.
		{"a complex result", "func() complex128", "b(nil, 2)", "return nil"},
		{"a Go named array result", "func() ID", "b(nil, 2)", "return nil"},
		{"a Go alias of a struct result", "func() Stamp", "b(nil, 2)", "return nil"},
		{"a Go alias of a named map result", "func() Header", "return nil", ""},
		{"a Go named slice result", "func() Names", "return nil", ""},
		{"a Go alias of a pointer to a struct result", "func() StampRef", "return nil", ""},
		{"a Go alias of a pointer to a local struct result", "func() SpotRef", "return nil", ""},
		{"a Go named complex result", "func() Phase", "b(nil, 2)", "return nil"},
		{"a Go alias of a named array result", "func() IDAlias", "b(nil, 2)", "return nil"},
		{"a Go alias of an anonymous struct result", "func() Unit", "b(nil, 2)", "return nil"},
		{"a generic Go alias of a pointer result", "func() Ref[Spot]", "return nil", ""},
		{"a generic Go alias of a scalar result", "func() Num[int]", "b(nil, 2)", "return nil"},
	}
	const bridge = `package main

import (
	"net/http"
	"time"
)

type ID [16]byte

type Stamp = time.Time

type Header = http.Header

type Names []string

type StampRef = *time.Time

type Spot struct{ X int }

type SpotRef = *Spot

type Phase complex64

type IDAlias = ID

type Unit = struct{}

type Ref[T any] = *T

type Num[T any] = int64

func Run(f func() struct{}, n int) int {
	if f == nil {
		return n
	}
	return 0
}

func Pair(f func() (int, error), n int) int {
	if f == nil {
		return n
	}
	return 0
}
`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bridge.go"), []byte(bridge), 0o600))
	body := func(t *testing.T, param, call string) string {
		input := `package main

import "io"
import "net/http"
import "time"

struct state(n int)

struct Empty()

sealed type Shape {
    case Dot()
    case Disk(r int)
}

var _t time.Time = time.Time{}

opaque type Count int

type Shower interface {
    Show() string
}

var _r io.Reader = nil
var _h http.Header = nil

func b(f ` + param + `, n int) int = if (f == nil) n else 0

func main() {
    Println(` + call + `)
}`
		mainPath := filepath.Join(dir, "main.gala")
		require.NoError(t, os.WriteFile(mainPath, []byte(input), 0o600))
		out, err := newThunkSugarTranspiler().Transpile(input, mainPath)
		require.NoError(t, err)
		return mainBodyOf(t, out)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := body(t, c.param, "b(nil, 2)")
			assert.Contains(t, got, c.want)
			if c.notWant != "" {
				assert.NotContains(t, got, c.notWant)
			}
		})
	}
	// #1466: `(nil)` is the same nil, and a Go signature's `struct{}`
	// result is a struct.
	calls := []struct{ name, param, call, want, notWant string }{
		{"a parenthesized nil", "func() state", "b((nil), 2)", "b((nil), 2)", "func() state"},
		{"a parenthesized nil for a pointer result", "func() *state", "b((nil), 2)", "return (nil)", "b((nil), 2)"},
		{"a parenthesized nil for several results", "func() state", "Pair((nil), 2)", "Pair((nil), 2)", "func() ("},
		{"a Go anonymous struct result", "func() state", "Run(nil, 2)", "Run(nil, 2)", "return nil"},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			got := body(t, c.param, c.call)
			assert.Contains(t, got, c.want)
			assert.NotContains(t, got, c.notWant)
		})
	}
}

// TestThunkSugar_ParenthesizedNilInAGenericCall pins #1466: a `(nil)`
// argument to a generic function binds no type parameter, as a bare `nil`
// does not, so both lower alike.
func TestThunkSugar_ParenthesizedNilInAGenericCall(t *testing.T) {
	lower := func(arg string) string {
		input := `package main

struct Box[T any](v T)

func mk[T any](f func() Box[T]) Option[Box[T]] = if (f == nil) None() else Some(f())

func main() {
    val b Option[Box[int]] = mk(` + arg + `)
    Println(b.IsEmpty())
}`
		out, err := newThunkSugarTranspiler().Transpile(input, "")
		require.NoError(t, err)
		return mainBodyOf(t, out)
	}
	bare := lower("nil")
	assert.Equal(t, bare, strings.ReplaceAll(lower("(nil)"), "(nil)", "nil"))
}
