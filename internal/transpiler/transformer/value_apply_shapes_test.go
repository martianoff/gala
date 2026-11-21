package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValueCalledThroughApply pins the call of a value whose type has an
// Apply, in every shape the value can take: it is lowered as `v.Apply(...)`
// written out is. A val's non-generic Apply used to leave a lambda argument
// untyped (GALA-E0033, #1279); a struct field or a call's result was called
// as a function Go does not have (#1280); type arguments for a value's
// generic Apply indexed the value (#1286); and a type whose no-parameter
// Apply is generic was called as a Go method it does not have (#1306).
func TestValueCalledThroughApply(t *testing.T) {
	const decls = `package main

struct Plain(n int)

func (p Plain) Apply(f func(int) int) int = f(p.n)

struct Add(n int)

func (a Add) Apply(x int) int = a.n + x

struct Show()

func (s Show) Apply[A any](a A) string = s"<$a>"

struct Missing()

func (m Missing) Apply[A any]() Option[A] = None[A]()

struct Holder(p Plain, var a Add, s Show)

struct Empty(tag int)

func (e Empty) Apply[A any]() Option[A] = None[A]()

struct Keeper(m Empty)

func makeAdd(n int) Add = Add(n)

struct Tick(n int)

func (k Tick) Apply() int = k.n

func makeTick() Tick = Tick(5)

struct Count(sep string)

func (c Count) Apply(parts ...string) int = parts.Size()

struct Inc(n int)

func (i Inc) Apply(x int = 1) int = i.n + x

func makeShow() Show = Show()

func main() {
`
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"a lambda argument to a val's Apply", `val p = Plain(1)
    Println(p((x) => x + 1))`, []string{`p.Apply(func(x int) int`}},
		{"a lambda passed by name to a val's Apply", `val p = Plain(1)
    Println(p(f = (x) => x + 1))`, []string{`p.Apply(func(x int) int`}},
		{"a lambda argument to a construction's Apply", `Println(Plain(2)((x) => x * 2))`, []string{`.Apply(func(x int) int`}},
		{"a val field", `val h = Holder(Plain(1), Add(2), Show())
    Println(h.p((x) => x))`, []string{`h.p.Get().Apply(func(x int) int`}},
		{"a var field", `val h = Holder(Plain(1), Add(2), Show())
    Println(h.a(3))`, []string{`h.a.Apply(3)`}},
		{"a field whose Apply is generic", `val h = Holder(Plain(1), Add(2), Show())
    Println(h.s(3))`, []string{`Show_Apply(h.s.Get(), 3)`}},
		{"a call's result", `Println(makeAdd(1)(2))`, []string{`makeAdd(1).Apply(2)`}},
		{"a call's result whose Apply is generic", `Println(makeShow()("x"))`, []string{`Show_Apply(makeShow(), "x")`}},
		{"type arguments for a val's generic Apply", `val s = Show()
    Println(s[int64](3))`, []string{`Show_Apply[int64](s, 3)`}},
		{"type arguments for a field's generic Apply", `val h = Holder(Plain(1), Add(2), Show())
    Println(h.s[int64](3))`, []string{`Show_Apply[int64](h.s.Get(), 3)`}},
		{"type arguments for a field's no-parameter generic Apply", `val k = Keeper(Empty(1))
    Println(k.m[int]())`, []string{`Empty_Apply[int](k.m.Get())`}},
		{"a call's result with no arguments", `Println(makeTick()())`, []string{`makeTick().Apply()`}},
		{"a val with no arguments for an Apply whose parameters all have defaults", `val i = Inc(1)
    Println(i())`, []string{`i.Apply(1)`}},
		{"a val with no arguments for an Apply whose only parameter is variadic", `val n = Count(",")
    Println(n())`, []string{`n.Apply()`}},
		{"a no-parameter generic Apply of a companion called with no arguments", `val n Option[int] = Missing()
    Println(n)`, []string{`Missing_Apply[int](Missing{})`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+"    "+tc.body+"\n}\n")
			require.NoError(t, err)
			for _, w := range tc.want {
				require.Contains(t, out, w)
			}
		})
	}

	// A method is called as one, not through the Apply of what it returns.
	out, err := transpileBareVariant(t, `package main

struct Add(n int)

func (a Add) Apply(x int) int = a.n + x

struct Box(n int)

func (b Box) Next(x int) Add = Add(b.n + x)

func main() {
    val b = Box(1)
    Println(b.Next(2)(3))
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "b.Next(2).Apply(3)")

	// The slot binds both a generic companion's type parameter and its
	// generic no-parameter Apply's own.
	out, err = transpileBareVariant(t, `package main

struct Pick[T any]()

func (p Pick[T]) Apply[A any]() Tuple[Option[T], Option[A]] = (None[T](), None[A]())

func main() {
    val x Tuple[Option[int], Option[string]] = Pick()
    Println(x)
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "Pick_Apply[string, int](Pick[int]{})")

	// Type arguments go to a generic Apply of a block-form struct with no
	// fields, and of a companion whose type takes none.
	out, err = transpileBareVariant(t, `package main

type Tag struct {}

func (g Tag) Apply[A any](a A) string = s"#$a"

struct Missing()

func (m Missing) Apply[A any]() Option[A] = None[A]()

func main() {
    val g = Tag{}
    Println(g[int64](3), Missing[int]())
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "Tag_Apply[int64](g, 3)")
	require.Contains(t, out, "Missing_Apply[int](Missing{})")

	// A misplaced spread names the value's type, through a call's result too.
	_, err = transpileBareVariant(t, `package main

import "martianoff/gala/go_interop"

struct Join()

func (j Join) Apply(parts ...string) int = parts.Size()

func mk() Join = Join()

func main() {
    Println(mk()("a", go_interop.SliceOf("b")...))
}
`)
	require.ErrorContains(t, err, `a spread argument must be the only argument of variadic parameter "parts" of Join.Apply`)

	// A function value held in a field, or returned by a call, is called as
	// the function it is.
	out, err = transpileBareVariant(t, `package main

struct Has(f func(int) int)

func mk() func(int) int = (x) => x + 1

func main() {
    val h = Has(mk())
    Println(mk()(3), h.f(4))
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "mk()(3)")
	require.Contains(t, out, "h.f.Get()(4)")

	// A field typed by its struct's type parameter is called through the
	// Apply of the type the receiver gives it.
	out, err = transpileBareVariant(t, `package main

struct Plain(n int)

func (p Plain) Apply(f func(int) int) int = f(p.n)

struct Box[T any](var v T)

func main() {
    val b = Box[Plain](Plain(1))
    Println(b.v((x) => x + 1))
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "b.v.Apply(func(x int) int")

	// A misplaced spread names the field's type too.
	_, err = transpileBareVariant(t, `package main

import "martianoff/gala/go_interop"

struct Join()

func (j Join) Apply(parts ...string) int = parts.Size()

struct Box(var j Join)

func main() {
    val b = Box(Join())
    Println(b.j("a", go_interop.SliceOf("b")...))
}
`)
	require.ErrorContains(t, err, `a spread argument must be the only argument of variadic parameter "parts" of Join.Apply`)

	// Another package's struct field is called through its own type's
	// Apply, not a same-named type's of this package.
	files := map[string]string{
		"go.mod":   "module example.com/fields\n\ngo 1.25\n",
		"gala.mod": "module example.com/fields\n",
		"lib/lib.gala": `package lib

struct Plain(n int)

func (p Plain) Apply(f func(int) int) int = f(p.n)

struct Holder(var P Plain, Q Plain)
`,
		"main.gala": `package main

import "example.com/fields/lib"

struct Plain(s string)

func (p Plain) Apply(x string) string = p.s + x

func main() {
    val h = lib.Holder(lib.Plain(1), lib.Plain(2))
    Println(h.P((x) => x + 1), h.Q((x) => x * 2), Plain("a")("b"))
}
`,
	}
	out, err = transpileInModule(t, files, "main.gala")
	require.NoError(t, err)
	require.Contains(t, out, "h.P.Apply(func(x int) int")
	require.Contains(t, out, "h.Q.Get().Apply(func(x int) int")
}
