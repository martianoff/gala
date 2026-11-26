package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValuelessLambdaForResultParam pins #1335: a lambda with no value passed
// for a parameter that takes a function with a result, whose type parameters
// only the lambda binds (`f func(A) B`), is a GALA error at the lambda. It
// used to reach go build ("does not match inferred type func(int) B").
func TestValuelessLambdaForResultParam(t *testing.T) {
	trans := newDefaultsTranspiler()
	cases := []struct {
		name, input, want string
	}{
		{"block ending in a void call", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val b = run((k int) => {
        Println(k)
    })
    b(1)
}`, "this lambda has no value, but run takes a function with a result here"},
		{"expression lambda of a void call", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val b = run((k int) => Println(k))
    b(1)
}`, "this lambda has no value, but run takes a function with a result here"},
		{"only the result is a type parameter", `package main

func run[B any](f func(int) B) B = f(1)

func main() {
    run((k int) => {
        Println(k)
    })
}`, "this lambda has no value, but run takes a function with a result here"},
		{"another argument binds the parameter type", `package main

func run[A any, B any](a A, f func(A) B) B = f(a)

func main() {
    run(1, (k int) => {
        Println(k)
    })
}`, "this lambda has no value, but run takes a function with a result here"},
		{"generic method", `package main

struct Box[T any](V T)

func (b Box[T]) Map[B any](f func(T) B) B = f(b.V)

func main() {
    Box(1).Map((k) => {
        Println(k)
    })
}`, "this lambda has no value, but Map takes a function with a result here"},
		{"declared result type", `package main

func run[A any](f func(A) int64) func(A) int64 = f

func main() {
    val b = run((k int) => {
        Println(k)
    })
    b(1)
}`, "this lambda returns int64, but its body can finish without a value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "valueless_lambda_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestLambdaForVoidParamKeepsWorking pins what #1335 leaves alone: a void
// function parameter takes a lambda with no value.
func TestLambdaForVoidParamKeepsWorking(t *testing.T) {
	trans := newDefaultsTranspiler()
	out, err := trans.Transpile(`package main

func each[A any](f func(A)) {
    var z A
    f(z)
}

func main() {
    each((k int) => {
        Println(k)
    })
}`, "valueless_lambda_test.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "func(k int) {")
}
