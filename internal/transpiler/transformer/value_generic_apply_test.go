package transformer_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// valueApplyMethodCall matches a call of the Go method Apply on a value the
// cases below call.
var valueApplyMethodCall = regexp.MustCompile(`\b(gg|b|sh|z|c|shower(\.Get\(\))?|Box\[string\]\{[^}]*\})\.Apply\(`)

// TestValueCalledThroughGenericApply pins the call of a value whose type has a
// generic Apply (one with its own type parameters). A generic method has no Go
// method: it lowers to a free function `Type_Apply(receiver, args...)`. Calling
// the value used to emit `v.Apply(...)`, which go build rejects ("v.Apply
// undefined"), and a lambda argument was lowered with no type to take. The
// call now goes where `v.Apply(...)` written out goes, with no arguments too,
// and the slot it fills binds a type parameter only Apply's result mentions.
// See #1272.
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

struct Zero(n int)

func (z Zero) Apply[A any]() Option[A] = None[A]()

struct Conv(n int)

func (c Conv) Apply[A any](x int) Option[A] = None[A]()

struct Cell[T any](v T)

func (c Cell[T]) Apply[A any](x T) Option[A] = None[A]()

val shower = Show()

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
		{
			"a package-level val",
			`Println(shower(3))`,
			[]string{`Show_Apply(shower.Get(), 3)`},
		},
		{
			"no arguments, the slot binding Apply's type parameter",
			`val z = Zero(1)
    val o Option[int] = z()
    Println(o)`,
			[]string{`Zero_Apply[int](z)`},
		},
		{
			"the slot binding a type parameter only Apply's result mentions",
			`val c = Conv(1)
    val q Option[string] = c(2)
    Println(q)`,
			[]string{`Conv_Apply[string](c, 2)`},
		},
		{
			"the slot binding it for a construction called at once",
			`val q Option[string] = Conv(1)(2)
    Println(q)`,
			[]string{`Conv_Apply[string](Conv{`},
		},
		{
			"the slot binding it for a construction with type arguments called at once",
			`val q Option[string] = Cell[int](1)(2)
    Println(q)`,
			[]string{`Cell_Apply[string`},
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
			// No value is called through a Go method Apply: `std.None[A]{}.Apply()`
			// in the declarations is a companion call.
			require.NotRegexp(t, valueApplyMethodCall, out)
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

	// Its arguments are checked against Apply's parameters, as a call of
	// `v.Apply(...)` written out is, rather than left for go build.
	for _, tc := range []struct{ name, body, want string }{
		{"no arguments for required parameters", "val gg = G[string]()\n    Println(gg())", `missing required argument "u" (parameter 1) in call to Apply`},
		{"an unknown parameter name", "val b = Box(5)\n    Println(b(g = (x) => x))", `unknown parameter "g" in call to Apply`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, src(tc.body))
			require.ErrorContains(t, err, tc.want)
		})
	}
}
