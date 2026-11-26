package transformer_test

import (
	"strings"
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
	// contains maps each expected text to the times it occurs: once per
	// lambda, so every branch is checked.
	cases := []struct {
		name, input string
		contains    map[string]int
	}{
		{"parenthesized block lambda", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val b = run(((k int) => {
        val q = k + 1
        q * q
    }))
    Println(b(3))
}`, map[string]int{"func(k int) int {": 1, "return q * q": 1}},
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
}`, map[string]int{"func(k int) int {": 2, "return q * q": 2}},
		{"if-expression whose branch is a block ending in the lambda", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val c = true
    val b = run(if (c) {
        Println("then")
        (k int) => {
            val q = k + 1
            q * q
        }
    } else (k int) => {
        val q = k - 1
        q * q
    })
    Println(b(3))
}`, map[string]int{"func(k int) int {": 2, "return q * q": 2}},
		{"if/else ending a branch block, and a parenthesized lambda branch", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val c = true
    val d = false
    val b = run(if (c) {
        Println("then")
        if (d) (k int) => {
            val q = k
            q * q
        } else (k int) => {
            val q = 0 - k
            q * q
        }
    } else ((k int) => {
        val q = k + 1
        q * q
    }))
    Println(b(3))
}`, map[string]int{"func(k int) int {": 3, "return q * q": 3}},
		{"match of block lambdas", `package main

func run[A any, B any](f func(A) B) func(A) B = f

func main() {
    val n = 2
    val b = run(n match {
        case 1 => (k int) => {
            val q = k + 1
            q * q
        }
        case _ => {
            Println("other")
            (k int) => {
                val q = k + n
                q * q
            }
        }
    })
    Println(b(3))
}`, map[string]int{"func(k int) int {": 2, "return q * q": 2}},
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
}`, map[string]int{"func(k int) int64 {": 2, "return 1\n": 1, "return 2\n": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "branch_lambda_test.gala")
			require.NoError(t, err)
			for want, n := range tc.contains {
				assert.Equal(t, n, strings.Count(out, want), "occurrences of %q in\n%s", want, out)
			}
		})
	}
}
