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
		{"a GALA function and a lambda to a GALA generic taking iter.Seq[T]", `package main

import "iter"

func gen(yield func(int) bool) {
    yield(1)
}

func countSeq[T any](s iter.Seq[T], p func(T) bool) int {
    var n = 0
    for x := range s {
        if p(x) {
            n = n + 1
        }
    }
    n
}

func main() {
    Println(countSeq(gen, (x) => x > 0))
}
`, []string{"func(x int) bool"}},
		{"a value converted to iter.Seq[string], read back", `package main

import "iter"
import "slices"

func main() {
    val u = iter.Seq[string]((yield) => {
        yield("a")
    })
    val k = () => u
    Println(slices.Collect(k()))
}
`, []string{"func() iter.Seq[string]"}},
		{"a placeholder lambda in a generic Go function slot", `package main

import "slices"
import "martianoff/gala/go_interop"

func main() {
    Println(slices.IndexFunc(go_interop.SliceOf(1, 2, 3), _ > 1))
}
`, []string{"int) bool"}},
		{"a GALA function to a GALA alias of iter.Seq[T]", `package main

import "iter"
import "slices"

type SeqOf[T any] iter.Seq[T]

func gen(yield func(int) bool) {
    yield(1)
}

func viaAlias[T any](s SeqOf[T]) []T = slices.Collect(s)

func main() {
    val a = viaAlias(gen)
    val k = () => a
    Println(k())
}
`, []string{"func() []int"}},
		{"an iter.Seq[int] value to a plain func parameter", `package main

import "slices"
import "martianoff/gala/go_interop"

func firstOf[T any](f func(func(T) bool)) []T {
    var out = go_interop.SliceEmpty[T]()
    f((x) => {
        out = go_interop.SliceAppend(out, x)
        false
    })
    out
}

func main() {
    val r = firstOf(slices.Values(go_interop.SliceOf(4, 5)))
    val k = () => r
    Println(k())
}
`, []string{"func() []int"}},
		{"a GALA function to a Sendable[iter.Seq[T]] parameter", `package main

import "iter"
import "slices"

func gen(yield func(int) bool) {
    yield(1)
}

func run[T any](s Sendable[iter.Seq[T]]) []T = slices.Collect(s)

func main() {
    val r = run(gen)
    val k = () => r
    Println(k())
}
`, []string{"func() []int"}},
		{"a GALA function to an iter.Seq[T] struct field", `package main

import "iter"

struct Holder[T any](Src iter.Seq[T])

func gen(yield func(int) bool) {
    yield(1)
}

func main() {
    val h = Holder(gen)
    Println(h.Src != nil)
}
`, []string{"Holder[int]"}},
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
