package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBranchLambdasBindCalleeTypeParams pins #1339: a lambda passed to a
// generic function whose type parameters only the lambda binds returns its
// trailing value also when it is parenthesized, or a branch of an if or match
// argument, as the bare lambda does (#1297).
func TestBranchLambdasBindCalleeTypeParams(t *testing.T) {
	trans := newDefaultsTranspiler()
	cases := []struct {
		name, input string
		contains    []string
	}{
		{"parenthesized block lambda", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val b = run(((k int) => {
        val q = k + 1
        q * q
    }))
    Println(b(3))
}`, []string{"func(k int) int {", "return q * q"}},
		{"if-expression of block lambdas", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val c = true
    val b = run(if (c) (k int) => {
        val q = k + 1
        q * q
    } else (k int) => {
        val q = k - 1
        q * q
    })
    Println(b(3))
}`, []string{"func(k int) int {", "return q * q"}},
		{"match of block lambdas", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val n = 2
    val b = run(n match {
        case 1 => (k int) => {
            val q = k + 1
            q * q
        }
        case _ => (k int) => {
            val q = k + n
            q * q
        }
    })
    Println(b(3))
}`, []string{"func(k int) int {", "return q * q"}},
		{"declared result through an if-expression", `package main

func run[A any](f func(A) int64) func(A) int64 = f

func main() {
    val c = false
    val b = run(if (c) (k int) => {
        Println(k)
        1
    } else (k int) => {
        Println(k)
        2
    })
    Println(b(3))
}`, []string{"func(k int) int64 {", "return 2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "branch_lambda_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
		})
	}
}
