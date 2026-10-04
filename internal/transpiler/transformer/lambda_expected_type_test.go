package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenericCtorLambdaTypeArgs covers lambdas passed to a generic struct
// constructor without explicit type arguments. The constructor's type
// arguments are inferred from the non-lambda arguments first, and the lambda
// is then lowered against the substituted field type — never against the
// declared `T`, which does not exist at the call site ("undefined: T").
func TestGenericCtorLambdaTypeArgs(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name: "named lambda takes T from a sibling named argument",
			input: `package main

struct Cell[T any](Value T, Map func(T) T)

func main() {
    val c = Cell(Value = 5, Map = (v) => v * 3)
    Println(c.Map(c.Value))
}`,
			contains: []string{"Cell[int]{", "func(v int) int {"},
		},
		{
			name: "positional lambda takes T from a sibling positional argument",
			input: `package main

struct Cell[T any](Value T, Map func(T) T)

func main() {
    val c = Cell(5, (v) => v * 3)
    Println(c.Map(c.Value))
}`,
			contains: []string{"Cell[int]{", "func(v int) int {"},
		},
		{
			name: "explicit type arguments reach a positional lambda",
			input: `package main

struct Cell[T any](Value T, Map func(T) T)

func main() {
    val c = Cell[int](5, (v) => v * 3)
    Println(c.Map(c.Value))
}`,
			contains: []string{"func(v int) int {"},
		},
		{
			name: "several type parameters",
			input: `package main

struct Conv[A any, B any](From A, To B, Fn func(A) B)

func main() {
    val c = Conv(From = 2, To = "x", Fn = (a) => s"n=$a")
    Println(c.Fn(c.From))
}`,
			contains: []string{"Conv[int, string]{", "func(a int) string {"},
		},
		{
			name: "type parameter bound only through a nested type",
			input: `package main

import . "martianoff/gala/collection_immutable"

struct Bag[T any](Items Array[T], Step func(T) T)

func main() {
    val b = Bag(Items = ArrayOf(1, 2), Step = (v) => v + 1)
    Println(b.Items.Map(b.Step))
}`,
			contains: []string{"Bag[int]{", "func(v int) int {"},
		},
		{
			name: "type parameter inferred from the lambda's own body",
			input: `package main

struct Gen[T any](Make func() T)

func main() {
    val g = Gen(Make = () => 5)
    Println(g.Make())
}`,
			contains: []string{"Gen[int]{", "func() int {"},
		},
		{
			name: "annotated lambda parameter binds T",
			input: `package main

struct Only[T any](F func(T) T)

func main() {
    val o = Only(F = (v int) => v + 1)
    Println(o.F(1))
}`,
			contains: []string{"Only[int]{", "func(v int) int {"},
		},
		{
			name: "named lambda for a generic function",
			input: `package main

func Ap[T any](x T, f func(T) T) T = f(x)

func main() {
    Println(Ap(f = (v) => v * 3, x = 5))
}`,
			contains: []string{"func(v int) int {"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "generic_ctor_lambda_test.gala")
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want)
			}
			assert.NotRegexp(t, `func\([a-z]+ [A-Z]\)`, body, "a lambda parameter kept a declared type parameter")
			assert.NotContains(t, body, "any", "a lambda was lowered against an unresolved type")
		})
	}
}

// TestSealedVariantLambdaTypeArgs covers the same two-phase inference for a
// case constructor of a generic sealed type.
func TestSealedVariantLambdaTypeArgs(t *testing.T) {
	trans := newDefaultsTranspiler()

	for _, ctor := range []string{"Ended(X = 5, F = (v) => v * 2)", "Ended(5, (v) => v * 2)"} {
		t.Run(ctor, func(t *testing.T) {
			out, err := trans.Transpile(`package main

sealed type Ev[T any] {
    case Ended(X T, F func(T) T)
    case Idle()
}

func run(e Ev[int]) int = e match {
    case Ended(x, f) => f(x)
    case Idle() => 0
}

func main() {
    Println(run(`+ctor+`))
}`, "sealed_variant_lambda_test.gala")
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			assert.Contains(t, body, "Ended[int]{}.Apply(")
			assert.Contains(t, body, "func(v int) int {")
		})
	}
}

// TestNestedIfInBranchLambdaBody pins the lambda boundary: an if-expression
// inside the body of a branch lambda is lowered against the lambda's own result
// type (`int`), never the slot type the branching expression fills
// (`func(int) int`).
func TestNestedIfInBranchLambdaBody(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "typed val, expression body",
			input: `package main

func main() {
    val c = true
    val f func(int) int = if (c) (x) => if (x > 0) x else 0 - x else (x) => x
    Println(f(-3))
}`,
		},
		{
			name: "expression-bodied function, block body",
			input: `package main

func mk(c bool) func(int) int = if (c) (x) => {
    val y = if (x > 0) x else 0 - x
    y
} else (x) => x

func main() {
    Println(mk(true)(-4))
}`,
		},
		{
			name: "return statement",
			input: `package main

func ret(c bool) func(int) int {
    return if (c) (x) => if (x > 0) x else 0 - x else (x) => x
}

func main() {
    Println(ret(true)(-5))
}`,
		},
		{
			name: "struct field, block body",
			input: `package main

struct Holder(F func(int) int)

func main() {
    val c = true
    val h = Holder(F = if (c) (x) => {
        val y = if (x > 0) x else 0 - x
        y
    } else (x) => x)
    Println(h.F(-6))
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "nested_if_lambda_test.gala")
			require.NoError(t, err)
			assert.Equal(t, 1, strings.Count(out, "func() func(int) int {"), "only the outer IIFE returns the slot type")
			assert.Contains(t, out, "func() int {", "the inner IIFE returns the lambda's result type")
		})
	}
}

// TestBranchLambdasWithUnboundTypeParams covers an if-expression or match of
// lambdas in a slot whose type still mentions an unbound type parameter: a
// generic constructor's (masked, then inferred from the branches) or a generic
// function's (filled with an `any` placeholder). The IIFE returns the branches'
// own type, never the unresolved slot type.
func TestBranchLambdasWithUnboundTypeParams(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name: "if-expression in a generic struct constructor",
			input: `package main

struct Pipe[A any, B any](Input A, Done func(A) B)

func main() {
    val up = true
    val p = Pipe(Input = 5, Done = if (up) (x) => x + 1 else (x) => x)
    Println(p.Done(p.Input))
}`,
			contains: []string{"Pipe[int, int]{", "func() func(int) int {"},
		},
		{
			name: "match in a generic struct constructor",
			input: `package main

struct Pipe[A any, B any](Input A, Done func(A) B)

func main() {
    val up = true
    val p = Pipe(Input = 5, Done = up match {
        case true => (x) => x * 2
        case _ => (x) => x
    })
    Println(p.Done(p.Input))
}`,
			contains: []string{"Pipe[int, int]{", "func(obj bool) func(int) int {"},
		},
		{
			name: "if-expression in a generic sealed-variant constructor",
			input: `package main

sealed type Job[A any, B any] {
    case Run(Input A, Done func(A) B)
    case Stop()
}

func runJob(j Job[int, int]) int = j match {
    case Run(i, d) => d(i)
    case Stop() => 0
}

func main() {
    val up = true
    Println(runJob(Run(Input = 5, Done = if (up) (x) => x + 3 else (x) => x)))
}`,
			contains: []string{"Run[int, int]{}.Apply(", "func() func(int) int {"},
		},
		{
			name: "if-expression for a generic function's partly bound parameter",
			input: `package main

func Apply[T any, R any](v T, f func(T) R) R = f(v)

func main() {
    val up = true
    Println(Apply(5, if (up) (x) => x + 1 else (x) => x - 1))
}`,
			contains: []string{"func() func(int) int {"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "unbound_branch_test.gala")
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want)
			}
			assert.NotContains(t, body, "any", "an unresolved slot type leaked into the IIFE")
			assert.NotRegexp(t, `func\(int\) [A-Z]\b`, body, "a type parameter leaked into the IIFE")
		})
	}
}

// TestStructTypeArgsFromGenericFields covers a generic struct literal whose
// type argument is bound only through fields of generic type (a sealed type, a
// generic struct, a nested generic struct value), or, failing that, by the
// slot type; an undeterminable one is a GALA error, never an uninstantiated
// `Holder{...}` literal.
func TestStructTypeArgsFromGenericFields(t *testing.T) {
	trans := newDefaultsTranspiler()
	decls := `package main

sealed type Mode[T any] {
    case ByInt(Fn func(int) T)
    case ByText(Fn func(string) T)
}

struct Inner[T any](X T)

struct Holder[T any](Md Mode[T], In Inner[T])

struct Outer[T any](H Holder[T])

struct Tag[T any](N int)

`
	cases := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name:     "named, sealed-variant and generic-struct fields",
			body:     `val h = Holder(Md = ByInt[int]((x) => x), In = Inner(X = 7))`,
			contains: []string{"Holder[int]{"},
		},
		{
			name:     "positional",
			body:     `val h = Holder(ByInt[int]((x) => x), Inner(7))`,
			contains: []string{"Holder[int]{"},
		},
		{
			name: "nested generic struct value",
			body: `val h = Holder(Md = ByInt[int]((x) => x), In = Inner(X = 7))
    val o = Outer(H = h)
    val o2 = Outer(h)`,
			contains: []string{"Outer[int]{"},
		},
		{
			name:     "type argument from the slot type",
			body:     `val g Tag[string] = Tag(N = 1)`,
			contains: []string{"Tag[string]{"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(decls+"func main() {\n    "+tc.body+"\n    Println(\"ok\")\n}", "generic_field_test.gala")
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want)
			}
			assert.NotRegexp(t, `\b(Holder|Outer|Tag)\{`, body, "an uninstantiated generic literal was emitted")
		})
	}

	_, err := trans.Transpile(decls+"func main() {\n    val g = Tag(N = 1)\n    Println(g.N)\n}", "generic_field_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot infer type argument T of generic struct Tag")
}

// TestUserWrittenAnySlot pins that a slot type the user wrote with `any` is
// not a placeholder: it is the IIFE's fallback when the branches' type cannot
// be inferred.
func TestUserWrittenAnySlot(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name: "uninferable branches fall back to a map[string]any result",
			input: `package main

func pickMap(c bool) map[string]any = if (c) nil else nil

func main() {
    Println(pickMap(true) == nil)
}`,
			contains: "return func() map[string]any {",
		},
		{
			name: "uninferable branches fall back to a []any result",
			input: `package main

func pickList(c bool) []any = if (c) nil else nil

func main() {
    Println(pickList(true) == nil)
}`,
			contains: "return func() []any {",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "user_any_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.contains)
		})
	}
}

// TestValueBlockSlotDoesNotLeak pins that a value block's slot applies to that
// block only: after a match arm block lowered against func(int) int, a later
// partial-function arm block's trailing if-expression is not lowered against
// it (and its trailing expression is the arm's value).
func TestValueBlockSlotDoesNotLeak(t *testing.T) {
	trans := newDefaultsTranspiler()

	out, err := trans.Transpile(`package main

import . "martianoff/gala/collection_immutable"

func main() {
    val c = true
    val g func(int) int = c match {
        case true => { (x) => x + 1 }
        case _ => (x) => x
    }
    val fs = ArrayOf(3, 7).Collect({
        case n if n > 0 => {
            if (n > 5) (s string) => s else (s string) => s + "!"
        }
    })
    Println(g(1))
    Println(fs.Map((f) => f("hi")))
}`, "block_slot_leak_test.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "func() func(string) string {")
	assert.Equal(t, 1, strings.Count(out, "func() func(int) int {")+strings.Count(out, "func(obj bool) func(int) int {"),
		"only the match lowers against func(int) int")
}

// TestAnnotatedLambdaResultIsItsReturnSlot pins that an explicit result type on
// a lambda is what a `return` in its body is lowered against.
func TestAnnotatedLambdaResultIsItsReturnSlot(t *testing.T) {
	trans := newDefaultsTranspiler()

	out, err := trans.Transpile(`package main

func main() {
    val g = (n int) func(int) int => {
        return if (n > 0) (x) => x + n else (x) => x - n
    }
    Println(g(4)(1))
}`, "annotated_lambda_test.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "return func(x int) int {")
}

// TestIfBetweenThunkReturningFunctions pins the function-type encoding for a
// function that returns a zero-parameter function: `func(int) func() int`, not
// `func(int, void) int`.
func TestIfBetweenThunkReturningFunctions(t *testing.T) {
	trans := newDefaultsTranspiler()

	out, err := trans.Transpile(`package main

func thunk(n int) func() int = () => n * 2

func main() {
    val c = true
    val g = if (c) thunk else thunk
    Println(g(7)())
}`, "thunk_if_test.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "func() func(int) func() int {")
}

// TestGenericCtorLambdaUninferableTypeArg pins the diagnostic for a lambda
// parameter whose type only the constructor's unbound type parameter could
// give: GALA-E0033 at the parameter, not Go's "undefined: T".
func TestGenericCtorLambdaUninferableTypeArg(t *testing.T) {
	trans := newDefaultsTranspiler()

	_, err := trans.Transpile(`package main

struct Only[T any](F func(T) T)

func main() {
    val o = Only(F = (v) => v)
    Println(o)
}`, "generic_ctor_lambda_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
	assert.Contains(t, err.Error(), `lambda parameter "v"`)
}

// TestGenericCtorPlaceholderLambdaTypeArgs covers placeholder lambdas (`_ * 10`)
// passed to a generic struct or sealed-variant constructor without explicit
// type arguments. They get the explicit lambda's treatment: the type
// parameters a sibling argument binds type the placeholders, the ones only the
// callback determines are inferred from its body — never the declared `A`
// ("undefined: A").
func TestGenericCtorPlaceholderLambdaTypeArgs(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name: "named placeholder takes A from a sibling and B from its body",
			input: `package main

struct Step[A any, B any](In A, Run func(A) B)

func main() {
    Println(Step(In = 4, Run = _ * 10).Run(4))
}`,
			contains: []string{"Step[int, int]{", "func(__p0 int) int {"},
		},
		{
			name: "positional placeholder",
			input: `package main

struct Step[A any, B any](In A, Run func(A) B)

func main() {
    Println(Step("go", _ + "!").Run("hi"))
}`,
			contains: []string{"Step[string, string]{", "func(__p0 string) string {"},
		},
		{
			name: "field typed by a generic function-type alias",
			input: `package main

type Conv[A any, B any] func(A) B

struct Step[A any, B any](In A, Run Conv[A, B])

func main() {
    Println(Step(In = 4, Run = _ * 10).Run(5))
}`,
			contains: []string{"Step[int, int]{", "func(__p0 int) int {"},
		},
		{
			name: "several placeholders",
			input: `package main

struct Fold[A any](Zero A, Combine func(A, A) A)

func main() {
    Println(Fold(Zero = 0, Combine = _ + _).Combine(3, 4))
}`,
			contains: []string{"Fold[int]{", "func(__p0 int, __p1 int) int {"},
		},
		{
			name: "sealed variant constructor",
			input: `package main

sealed type Job[A any, B any] {
    case Mapper(Input A, Fn func(A) B)
}

func main() {
    val m = Mapper(Input = 3, Fn = _ * 2)
    Println(m.Fn(m.Input))
}`,
			contains: []string{"Mapper[int, int]{}.Apply(", "func(__p0 int) int {"},
		},
		{
			name: "generic function",
			input: `package main

func apply[A any, B any](x A, f func(A) B) B = f(x)

func main() {
    Println(apply(6, _ * 7))
}`,
			contains: []string{"func(__p0 int) int {"},
		},
		{
			name: "a placeholder of a nested call's own slot still binds the type arguments",
			input: `package main

struct Step[A any, B any](In A, Run func(A) B)

func compose(f func(int) int, g func(int) string) func(int) string = (x) => g(f(x))

func show(n int) string = s"n=$n"

func main() {
    Println(Step(In = 4, Run = compose(_ + 1, show)).Run(4))
}`,
			contains: []string{"Step[int, string]{", "compose(func(__p0 int) int {"},
		},
		{
			name: "a placeholder inside a nested call's non-function argument is the field's own",
			input: `package main

struct Step[A any, B any](In A, Run func(A) B)

func double(n int) int = n * 2

func main() {
    Println(Step(In = 4, Run = double(_)).Run(4))
}`,
			contains: []string{"Step[int, int]{", "func(__p0 int) int {"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "generic_ctor_placeholder_test.gala")
			require.NoError(t, err)
			body := out[strings.Index(out, "func main()"):]
			for _, want := range tc.contains {
				assert.Contains(t, body, want)
			}
			assert.NotRegexp(t, `__p0 [A-Z]\)`, body, "a placeholder kept a declared type parameter")
			assert.NotContains(t, body, "any", "a placeholder was lowered against an unresolved type")
		})
	}
}

// TestGenericCtorPlaceholderUninferableTypeArg pins the diagnostic for a
// placeholder whose type only the constructor's unbound type parameter could
// give: GALA-E0033, as for an unannotated lambda parameter, not Go's
// "undefined: A".
func TestGenericCtorPlaceholderUninferableTypeArg(t *testing.T) {
	trans := newDefaultsTranspiler()

	_, err := trans.Transpile(`package main

struct Step[A any, B any](In A, Run func(A) B)

func main() {
    val s = Step(Run = _ * 10)
    Println(s)
}`, "generic_ctor_placeholder_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
	assert.Contains(t, err.Error(), "placeholder `_` has no type")

	// More placeholders than the function type has parameters is the same
	// GALA-E0033 a lambda of too many parameters gets, not an `any` parameter.
	_, err = trans.Transpile(`package main

struct Fold[A any](Zero A, Combine func(A, A) A)

func main() {
    Println(Fold(Zero = 0, Combine = _ + _ + _).Combine(1, 2))
}`, "generic_ctor_placeholder_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0033")
	assert.Contains(t, err.Error(), "placeholder lambda has 3 parameters where a function of 2 is expected")
}

// TestBranchLambdasTakeSlotType covers an if-expression or match whose
// branches are lambdas, standing in a function-typed slot. The IIFE that
// lowers the branching expression returns the slot's function type (not the
// lambdas' result type), and each branch lambda takes its parameter types from
// the slot.
func TestBranchLambdasTakeSlotType(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name: "zero-parameter lambdas in a struct field",
			input: `package main

struct Box(N int, F func() int)

func main() {
    val up = true
    val b = Box(N = 1, F = if (up) () => 3 else () => 4)
    Println(b.F())
}`,
			contains: []string{"F: std.NewImmutable(func() func() int {"},
		},
		{
			name: "untyped-parameter lambdas in a function argument",
			input: `package main

func apply(f func(int) int, x int) int = f(x)

func main() {
    val up = true
    Println(apply(if (up) (x) => x * 5 else (x) => x * 6, 1))
}`,
			contains: []string{"apply(func() func(int) int {", "return func(x int) int {"},
		},
		{
			name: "else-if chain",
			input: `package main

struct Op(F func(int) int)

func main() {
    val n = 2
    val o = Op(F = if (n == 1) (x) => x else if (n == 2) (x) => x * 2 else (x) => x * 3)
    Println(o.F(1))
}`,
			contains: []string{"func() func(int) int {", "return x * 2"},
		},
		{
			name: "block branches",
			input: `package main

struct Op(F func(int) int)

func main() {
    val up = true
    val o = Op(F = if (up) { (x) => x + 7 } else { (x) => x + 8 })
    Println(o.F(1))
}`,
			contains: []string{"func() func(int) int {", "return func(x int) int {"},
		},
		{
			name: "return statement",
			input: `package main

func pick(up bool) func(int) int {
    return if (up) (x) => x + 1 else (x) => x - 1
}

func main() {
    Println(pick(true)(1))
}`,
			contains: []string{"return func(x int) int {"},
		},
		{
			name: "match arms in a struct field",
			input: `package main

struct Op(F func(int) int)

func main() {
    val up = true
    val o = Op(F = up match {
        case true => (x) => x + 100
        case _ => (x) => x + 200
    })
    Println(o.F(1))
}`,
			contains: []string{"func(obj bool) func(int) int {", "return func(x int) int {"},
		},
		{
			name: "match block arms",
			input: `package main

struct Op(F func(int) int)

func main() {
    val up = true
    val o = Op(F = up match {
        case true => { (x) => x + 1 }
        case _ => { (x) => x + 2 }
    })
    Println(o.F(1))
}`,
			contains: []string{"func(obj bool) func(int) int {", "return func(x int) int {"},
		},
		{
			name: "match arms in a typed val",
			input: `package main

func main() {
    val up = true
    val f func(int) int = up match {
        case true => (x) => x + 7
        case _ => (x) => x - 7
    }
    Println(f(1))
}`,
			contains: []string{"func(obj bool) func(int) int {", "return func(x int) int {"},
		},
		{
			name: "match arms in an expression-bodied function",
			input: `package main

func pick(up bool) func(int) int = up match {
    case true => (x) => x * 10
    case _ => (x) => x * 20
}

func main() {
    Println(pick(false)(1))
}`,
			contains: []string{"func(obj bool) func(int) int {", "return func(x int) int {"},
		},
		{
			name: "match nested in an if branch",
			input: `package main

struct Op(F func(int) int)

func main() {
    val up = true
    val o = Op(F = if (up) up match {
        case true => (x) => x + 60
        case _ => (x) => x
    } else (x) => x)
    Println(o.F(1))
}`,
			contains: []string{"func() func(int) int {", "func(obj bool) func(int) int {"},
		},
		{
			name: "if-expression as a Copy override",
			input: `package main

struct Box(N int, F func() int)

func main() {
    val up = true
    val b = Box(N = 1, F = () => 1)
    val c = b.Copy(F = if (up) () => 3 else () => 4)
    Println(c.F())
}`,
			contains: []string{"func() func() int {"},
		},
		{
			name: "match as a Copy override",
			input: `package main

struct Op(F func(int) int)

func main() {
    val up = true
    val o = Op(F = (x) => x)
    val c = o.Copy(F = up match {
        case true => (x) => x + 1
        case _ => (x) => x
    })
    Println(c.F(1))
}`,
			contains: []string{"func(obj bool) func(int) int {", "return func(x int) int {"},
		},
		{
			name: "branch lambdas in a generic constructor",
			input: `package main

struct Cell[T any](Value T, Map func(T) T)

func main() {
    val up = true
    val c = Cell(Value = 2, Map = if (up) (v) => v + 1 else (v) => v - 1)
    Println(c.Map(c.Value))
}`,
			contains: []string{"Cell[int]{", "func() func(int) int {", "return func(v int) int {"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "branch_lambda_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			assert.NotRegexp(t, `func\([a-z]+ any\)`, out, "a branch lambda was lowered without the slot type")
		})
	}
}
