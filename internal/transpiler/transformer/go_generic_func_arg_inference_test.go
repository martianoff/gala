package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoGenericInferenceThroughDefinedFuncTypes pins #1318 and #1321: a
// generic Go parameter of a defined function type (`iter.Seq[E]`) is typed
// through its underlying function type. A GALA function passed there binds
// E (it was left open, and `[]E` reached the generated Go), and a lambda
// passed there takes its parameter types from it (it was GALA-E0033).
//
// Hermetic: slices and maps are Go stdlib, resolved via the Go SDK.
func TestGoGenericInferenceThroughDefinedFuncTypes(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"a GALA function passed as iter.Seq[E]", `package main

import "slices"

func gen(yield func(int) bool) {
    yield(1)
}

func main() {
    val xs = slices.Collect(gen)
    val h = () => xs
    Println(h())
}
`, []string{"func() []int"}},
		{"a GALA function passed as iter.Seq2[K, V]", `package main

import "maps"

func pairs(yield func(string, int) bool) {
    yield("a", 1)
}

func main() {
    val m = maps.Collect(pairs)
    val h = () => m
    Println(h())
}
`, []string{"func() map[string]int"}},
		{"a lambda passed as iter.Seq[string]", `package main

import "slices"

func main() {
    Println(slices.Collect[string]((yield) => {
        yield("a")
    }))
}
`, []string{"func(yield func(string) bool)"}},
		{"a GALA function passed to a GALA generic taking iter.Seq[T]", `package main

import "iter"
import "slices"

func gen(yield func(int) bool) {
    yield(1)
}

func wrap[T any](s iter.Seq[T]) []T = slices.Collect(s)

func main() {
    val r = wrap(gen)
    val h = () => r
    Println(h())
}
`, []string{"func() []int"}},
		{"a lambda converted to iter.Seq[string]", `package main

import "iter"
import "slices"

func main() {
    val s = iter.Seq[string]((yield) => {
        yield("a")
    })
    Println(slices.Collect(s))
}
`, []string{"func(yield func(string) bool)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.NoError(t, err)
			for _, w := range tc.want {
				require.Contains(t, out, w)
			}
		})
	}
}
