package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoNamedTypeConversionAndIndex pins #1311: a conversion to a Go named
// type (`sort.StringSlice(xs)`) is typed as that type, and indexing a Go
// type defined over a slice or map (`q["a"]` on url.Values) is typed as its
// element. Both were untyped: a lambda over the value got no parameter
// types (GALA-E0033), and a lambda returning it was emitted as `func() q[any]`.
//
// Hermetic: sort, net/url and slices are Go stdlib, resolved via the Go SDK.
func TestGoNamedTypeConversionAndIndex(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"conversion to a Go named slice type", `package main

import "slices"
import "sort"
import "martianoff/gala/go_interop"

func main() {
    val ss = sort.StringSlice(go_interop.SliceOf("b", "a"))
    Println(slices.IndexFunc(ss, (x) => x == "a"))
}
`, []string{"func(x string) bool"}},
		{"conversion to a Go named integer type", `package main

import "time"

func main() {
    val d = time.Duration(5)
    val f = () => d
    Println(f())
}
`, []string{"func() time.Duration"}},
		{"index of a Go named map type", `package main

import "net/url"

func main() {
    val q = url.Values{}
    val vs = q["a"]
    val f = () => vs
    Println(f())
}
`, []string{"func() []string"}},
		{"index of a Go named slice type", `package main

import "sort"
import "martianoff/gala/go_interop"

func main() {
    val ss = sort.StringSlice(go_interop.SliceOf("b"))
    val f = () => ss[0]
    Println(f())
}
`, []string{"func() string"}},
		{"conversion through an aliased Go import, then indexed", `package main

import gourl "net/url"

func main() {
    val q = gourl.Values{}
    val f = () => gourl.Values(q)["a"]
    Println(f())
}
`, []string{"func() []string"}},
		{"index through a GALA alias of a Go named map type", `package main

import "net/url"

type Tags url.Values

func main() {
    val q Tags = url.Values{}
    val f = () => q["a"]
    Println(f())
}
`, []string{"func() []string"}},
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
