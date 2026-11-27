package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoUntypedConstArithmeticType pins #1440: arithmetic between an untyped
// constant of a Go package (`math.MaxInt8`, `http.StatusOK`) and a typed
// operand has the typed operand's type, as in Go. It was typed by the
// constant's default (`int`), so a generic call seeded by such a value bound
// its type parameter to `int` and go build rejected the lambda.
func TestGoUntypedConstArithmeticType(t *testing.T) {
	trans := newDefaultsTranspiler()
	cases := []struct {
		name, input string
		contains    []string
	}{
		{"FoldLeft seeded by a Duration", `package main

import "time"
import "math"
import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2)
    val total = xs.FoldLeft(math.MaxInt8 * time.Second, (acc, x) => acc + time.Duration(x) * time.Second)
    Println(total)
}`, []string{"func(acc time.Duration, x int) time.Duration {"}},
		{"generic function seeded by a Duration", `package main

import "time"
import "net/http"

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    val d = apply(http.StatusOK * time.Millisecond, (x) => x * 2)
    Println(d)
}`, []string{"func(x time.Duration) time.Duration {"}},
		{"constant on the right keeps the typed left operand", `package main

import "time"
import "math"

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    val d = apply(time.Second * math.MaxInt8, (x) => x * 2)
    Println(d)
}`, []string{"func(x time.Duration) time.Duration {"}},
		{"a literal times a Go float constant is a float64", `package main

import "math"

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    Println(apply(2 * math.Pi, (x) => x / 3))
}`, []string{"func(x float64) float64 {"}},
		{"a Go constant times a float literal is a float64", `package main

import "math"

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    Println(apply(math.MaxInt8 * 0.5, (x) => x))
}`, []string{"func(x float64) float64 {"}},
		{"a Go constant shifted by a typed count keeps its default", `package main

import "math"

func apply[T any](v T, f func(T) T) T = f(v)

func main() {
    val n uint = 3
    Println(apply(math.MaxInt8 << n, (x) => x + 1))
}`, []string{"func(x int) int {"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "go_const_arith_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
		})
	}
}
