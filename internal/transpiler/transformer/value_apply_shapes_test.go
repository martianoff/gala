package transformer_test

import (
	"strings"
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

	// A pointer-receiver Apply of a field of a call's result, or of a call's
	// result, is called on an addressed copy, as `recv.Apply(...)` written
	// out is (#1389); one that can change its receiver is GALA-E0110.
	const counter = `package main

struct Counter(var n int)

func (c *Counter) Apply(x int) int = c.n + x

struct Box(var h Counter)

func mkBox() Box = Box(Counter(1))

func mkCounter() Counter = Counter(1)

func main() {
`
	out, err = transpileBareVariant(t, counter+"    Println(mkBox().h(3), mkCounter()(3), mkBox().h.Apply(3))\n}\n")
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(out, "std.AddrOfCopy(mkBox().h).Apply(3)"))
	require.Contains(t, out, "std.AddrOfCopy(mkCounter()).Apply(3)")
	// So is one called with no arguments, its defaults filled.
	out, err = transpileBareVariant(t, `package main

struct Counter(var n int)

func (c *Counter) Apply(x int = 5) int = c.n + x

func mkCounter() Counter = Counter(1)

struct Box(var h Counter)

func mkBox() Box = Box(Counter(1))

struct Wrap[T any](var v T)

func mkWrap() Wrap[Counter] = Wrap(Counter(1))

func main() {
    Println(mkCounter()(), mkBox().h(), mkWrap().v(3))
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "std.AddrOfCopy(mkCounter()).Apply(5)")
	require.Contains(t, out, "std.AddrOfCopy(mkBox().h).Apply(5)")
	require.Contains(t, out, "std.AddrOfCopy(mkWrap().v).Apply(3)")

	// A lambda argument still takes Apply's parameter type through the copy.
	out, err = transpileBareVariant(t, `package main

struct Gen[T any](var v T)

func (g *Gen[T]) Apply(f func(T) T) T = f(g.v)

func mk() Gen[int] = Gen(1)

struct Shelf(var g Gen[int])

func main() {
    Println(mk()((x) => x + 1), Shelf(mk()).g((x) => x * 2))
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "std.AddrOfCopy(mk()).Apply(func(x int) int")
	require.Contains(t, out, ".g).Apply(func(x int) int")
	mutating := strings.Replace(counter, "= c.n + x", "{\n    c.n = c.n + x\n    c.n\n}", 1)
	_, err = transpileBareVariant(t, mutating+"    Println(mkCounter()(3))\n}\n")
	require.ErrorContains(t, err, "GALA-E0110")
	// A field reached through a val is the val's, which no Apply may change,
	// as `b.h.Apply(3)` written out may not.
	_, err = transpileBareVariant(t, mutating+"    val b = mkBox()\n    Println(b.h(3))\n}\n")
	require.ErrorContains(t, err, "GALA-E0110")

	// A val, a package-level val and a construction called through a
	// pointer-receiver Apply follow the written-out `v.Apply(...)` rules too
	// (#1398): an addressed copy for one that only reads its receiver,
	// GALA-E0110 for one that changes it; a var is called in place.
	out, err = transpileBareVariant(t, `package main

struct Counter(var n int)

func (c *Counter) Apply(x int) int = c.n + x

val pc = Counter(2)

func main() {
    val c = Counter(1)
    var v = Counter(3)
    Println(c(3), pc(1), Counter(5)(3), v(4))
}
`)
	require.NoError(t, err)
	for _, w := range []string{"std.AddrOfCopy(c).Apply(3)", "std.AddrOfCopy(pc.Get()).Apply(1)", "std.AddrOfCopy(Counter{n: 5}).Apply(3)", "v.Apply(4)"} {
		require.Contains(t, out, w)
	}
	_, err = transpileBareVariant(t, mutating+"    val b = Counter(1)\n    Println(b(3))\n}\n")
	require.ErrorContains(t, err, "GALA-E0110")

	// An inout place is not lent to an Apply that may keep its address, as
	// with `k.Apply(1)` written out (GALA-E0113).
	for _, use := range []string{
		"func lend(inout k Keeper) = Println(k(1).n)\n\nfunc main() {\n    var k = Keeper(1)\n    lend(&k)\n}\n",
		"func lend(inout h Hold) = Println(h.k(1).n)\n\nfunc main() {\n    var h = Hold(Keeper(1))\n    lend(&h)\n}\n",
	} {
		_, err = transpileBareVariant(t, `package main

struct Keeper(var n int)

func (k Ptr[Keeper]) Apply(x int) Ptr[Keeper] = k

struct Hold(var k Keeper)

`+use)
		require.ErrorContains(t, err, "GALA-E0113", use)
	}
	// A val that must not be copied is GALA-E0053, as `g.Apply(1)` is.
	_, err = transpileBareVariant(t, `package main

import "sync"

struct Guarded(var mu sync.Mutex, var n int)

func (g *Guarded) Apply(x int) int = g.n + x

func main() {
    val g = Guarded(sync.Mutex{}, 1)
    Println(g(1))
}
`)
	require.ErrorContains(t, err, "GALA-E0053")

	// A pointer is called through the Apply of the type it points to, as
	// `p.Apply(...)` written out is; one that may be nil fills no Ptr
	// receiver (GALA-E0080, #1445).
	out, err = transpileBareVariant(t, `package main

struct Node(var v int)

func (n *Node) Apply(x int) int = n.v + x

struct Plain(var v int)

func (n Plain) Apply(x int) int = n.v + x

struct Hold(var p *Node, var q *Plain)

func ptrOf(n Node) *Node {
    var c = n
    &c
}

func main() {
    var n = Node(1)
    var m = Plain(2)
    val h = Hold(&n, &m)
    val r = &n
    Println(h.p(1), h.q(3), r(5), ptrOf(Node(7))(1))
}
`)
	require.NoError(t, err)
	for _, w := range []string{"h.p.Apply(1)", "h.q.Apply(3)", "r.Apply(5)", "ptrOf(Node{v: 7}).Apply(1)"} {
		require.Contains(t, out, w)
	}
	for _, call := range []string{"Hold(mk()).p(1)", "r(1)", "mk()(1)", "z()", "w(1)"} {
		_, err = transpileBareVariant(t, `package main

struct Node(var v int)

func (n Ptr[Node]) Apply(x int) int = n.v + x

struct Zero(var v int)

func (z Ptr[Zero]) Apply() int = z.v

type NodeRef *Node

struct Hold(var p *Node)

func mk() *Node = nil

func mkZero() *Zero = nil

func main() {
    val r = mk()
    val z = mkZero()
    val w NodeRef = mk()
    Println(`+call+`)
}
`)
		require.ErrorContains(t, err, "GALA-E0080", call)
	}

	// So do they with no arguments.
	out, err = transpileBareVariant(t, `package main

struct Tick(var n int)

func (t *Tick) Apply() int = t.n

func lend(inout t Tick) int = t()

func main() {
    val a = Tick(1)
    var b = Tick(2)
    Println(a(), Tick(3)(), lend(&b))
}
`)
	require.NoError(t, err)
	for _, w := range []string{"std.AddrOfCopy(a).Apply()", "std.AddrOfCopy(Tick{n: 3}).Apply()", "(*t).Apply()"} {
		require.Contains(t, out, w)
	}
	_, err = transpileBareVariant(t, `package main

struct Keeper(var n int)

func (k Ptr[Keeper]) Apply() Ptr[Keeper] = k

func lend(inout k Keeper) = Println(k().n)

func main() {
    var k = Keeper(1)
    lend(&k)
}
`)
	require.ErrorContains(t, err, "GALA-E0113")

	// One that only reads its receiver is called on the place.
	out, err = transpileBareVariant(t, `package main

struct Plain(var n int)

func (p Plain) Apply(x int) int = p.n + x

func lend(inout p Plain) = Println(p(1))

func main() {
    var p = Plain(1)
    lend(&p)
}
`)
	require.NoError(t, err)
	require.Contains(t, out, "(*p).Apply(1)")

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
