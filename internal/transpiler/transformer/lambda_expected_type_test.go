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
