package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRangeOverGoNamedAndIterTypes pins the types `for k, v := range x` binds
// when x is a Go named map type, a Go iterator (iter.Seq, iter.Seq2) or a
// predeclared integer type other than int (#1299). Each was typed with an
// int key and an untyped value: `s"$k"` chose %d for a string key, and
// `k + "!"` was rejected with GALA-E0091. The generic iterators (maps.All,
// maps.Keys, slices.All) bind K, V and E through their `~map[K]V` and
// `~[]E` constraints.
//
// Hermetic: net/url, net/http, maps and slices are Go stdlib, resolved via
// the Go SDK.
func TestRangeOverGoNamedAndIterTypes(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"a named map type (url.Values)", `package main

import "net/url"

func main() {
    val q = url.Values{}
    for k, v := range q {
        Println(s"$k=$v")
    }
}
`, []string{`"%s=%v"`}},
		{"a named map type (http.Header)", `package main

import "net/http"

func main() {
    val h = http.Header{}
    for k, vs := range h {
        Println(k + "!", vs[0] + "?")
    }
}
`, []string{`k+"!"`, `vs[0]+"?"`}},
		{"an iter.Seq2 (maps.All)", `package main

import "maps"
import "martianoff/gala/go_interop"

func main() {
    val m = go_interop.MapPut(go_interop.MapEmpty[string, float64](), "a", 1.5)
    for k, v := range maps.All(m) {
        Println(s"$k $v", k + "!", k.Size(), v * 2.0)
    }
}
`, []string{`"%s %g"`, `k+"!"`, `utf8.RuneCountInString(k)`}},
		{"an iter.Seq (maps.Keys)", `package main

import "maps"
import "martianoff/gala/go_interop"

func main() {
    val m = go_interop.MapPut(go_interop.MapEmpty[string, int](), "a", 1)
    for k := range maps.Keys(m) {
        Println(s"$k")
    }
}
`, []string{`"%s", k`}},
		{"an iter.Seq2 with an int key (slices.All)", `package main

import "slices"
import "martianoff/gala/go_interop"

func main() {
    for i, w := range slices.All(go_interop.SliceOf("x")) {
        Println(s"$i:$w")
    }
}
`, []string{`"%d:%s", i, w`}},
		{"an int64", `package main

func main() {
    val n = int64(3)
    for i := range n {
        val f = () => i
        Println(f())
    }
}
`, []string{"func() int64"}},
		// Go binds i to rune: typed int32 (the same type), so `s"$i"` keeps %d, not %c.
		{"an untyped rune constant (unicode.MaxASCII)", `package main

import "unicode"

func main() {
    for i := range unicode.MaxASCII {
        val f = () => i
        Println(s"i=$i", f())
    }
}
`, []string{`"i=%d"`, "func() int32"}},
		// Go binds i to the defined type itself (#1317), so `s"$i"` uses %v
		// and the type's String method, and its methods are callable.
		{"a Go defined integer type (time.Duration)", `package main

import "time"

func main() {
    val d = time.Duration(3)
    for i := range d {
        val f = () => i
        Println(s"i=$i", f(), i.Nanoseconds())
    }
}
`, []string{`"i=%v"`, "func() time.Duration", "i.Nanoseconds()"}},
		// An opaque type is a Go defined type: i is bound to it. A `type`
		// declaration is an alias: i is bound to the aliased type.
		{"an opaque type over an integer type", `package main

import "time"

opaque type Millis int64

opaque type Wait time.Duration

type Count int64

func main() {
    for i := range Millis(3) {
        val f = () => i
        Println(f())
    }
    for w := range Wait(2) {
        val g = () => w
        Println(g())
    }
    for c := range Count(2) {
        val h = () => c
        Println(h())
    }
}
`, []string{"func() Millis", "func() Wait", "func() int64"}},
		// An opaque type over string ranges as a string: an int key and a rune.
		{"an opaque type over string", `package main

opaque type Name string

func main() {
    for i, c := range Name("ab") {
        val f = () => c
        Println(i, f())
    }
}
`, []string{"func() rune"}},
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

// TestLambdaToGoGenericWithCoreTypeConstraint pins #1298: a lambda passed to
// a generic Go function whose slice or map parameter has a type-set
// constraint (`S ~[]E`, `M ~map[K]V`) takes its parameter types from the
// core type the bound argument matches, as Go's constraint type inference
// does. It was GALA-E0033 ("lambda parameter has no type").
func TestLambdaToGoGenericWithCoreTypeConstraint(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"slices.IndexFunc", `package main

import "slices"
import "martianoff/gala/go_interop"

func main() {
    val xs = go_interop.SliceOf(3, 1, 2)
    Println(slices.IndexFunc(xs, (x) => x < 3))
}
`, []string{"func(x int) bool"}},
		{"slices.SortFunc", `package main

import "slices"
import "martianoff/gala/go_interop"

func main() {
    val xs = go_interop.SliceOf("b", "a")
    slices.SortFunc(xs, (a, b) => a.Size() - b.Size())
}
`, []string{"func(a string, b string) int"}},
		{"maps.DeleteFunc", `package main

import "maps"
import "martianoff/gala/go_interop"

func main() {
    val m = go_interop.MapPut(go_interop.MapEmpty[string, int](), "a", 1)
    maps.DeleteFunc(m, (k, v) => k == "a" && v > 0)
}
`, []string{"func(k string, v int) bool"}},
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
