package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValueCalledThroughGenericApply pins the call of a value whose type has a
// generic Apply (one with its own type parameters). A generic method has no Go
// method: it lowers to a free function `Type_Apply(receiver, args...)`. Calling
// the value used to emit `v.Apply(...)`, which go build rejects ("v.Apply
// undefined"), and a lambda argument was lowered with no type to take. The
// call now goes where `v.Apply(...)` written out goes. See #1272.
func TestValueCalledThroughGenericApply(t *testing.T) {
	const decls = `
struct G[T any]()

func (g G[T]) Apply[U any](u U, xs ...T) int = xs.Size()

struct Box[T any](v T)

func (b Box[T]) Apply[U any](f func(T) U) U = f(b.v)

struct Show()

func (s Show) Apply[A any](a A) string = s"<$a>"

struct Plain(n int)

func (p Plain) Apply(x int) int = p.n + x

func main() {
`
	cases := []struct {
		name, body string
		want       []string
	}{
		{
			"a val of a generic type, a spread into Apply's variadic parameter",
			`val gg = G[string]()
    Println(gg(1, go_interop.SliceOf("a")...))`,
			[]string{`G_Apply(gg, 1, go_interop.SliceOf("a")...)`},
		},
		{
			"a val of a generic type, variadic arguments",
			`val gg = G[string]()
    Println(gg("u", "a", "b"))`,
			[]string{`G_Apply(gg, "u", "a", "b")`},
		},
		{
			"a lambda typed by the receiver's type argument",
			`val b = Box(5)
    Println(b((x) => x * 2))`,
			[]string{`Box_Apply(b, func(x int) int`},
		},
		{
			"a lambda passed by name",
			`val b = Box(5)
    Println(b(f = (x) => x + 1))`,
			[]string{`Box_Apply(b, func(x int) int`},
		},
		{
			"a construction called at once",
			`Println(Box("hi")((s) => s.Size()))`,
			[]string{`Box_Apply(`, `func(s string) int`},
		},
		{
			"a var of a non-generic type",
			`var sh = Show()
    Println(sh(3), sh("x"))`,
			[]string{`Show_Apply(sh, 3)`, `Show_Apply(sh, "x")`},
		},
	}
	// The import goes only where it is used: GALA reports an unused one.
	src := func(body string) string {
		header := "package main\n"
		if strings.Contains(body, "go_interop.") {
			header += "\nimport \"martianoff/gala/go_interop\"\n"
		}
		return header + decls + "    " + body + "\n}\n"
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, src(tc.body))
			require.NoError(t, err)
			for _, w := range tc.want {
				require.Contains(t, out, w)
			}
			require.NotContains(t, out, ".Apply(")
		})
	}

	// A non-generic Apply is still a Go method, called as one.
	out, err := transpileBareVariant(t, src("val p = Plain(1)\n    Println(p(2))"))
	require.NoError(t, err)
	require.Contains(t, out, "p.Apply(2)")

	// A spread of other elements is still GALA-E0138, against the value's
	// type argument.
	_, err = transpileBareVariant(t, src(`val gg = G[string]()
    Println(gg(1, go_interop.SliceOf(1)...))`))
	require.ErrorContains(t, err, "cannot spread `go_interop.SliceOf(1)` of type []int into a variadic parameter of string")
}
