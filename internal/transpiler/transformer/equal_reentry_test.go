package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestEqualReentry pins GALA-E0108: a type's own Equal that passes its own
// type, or a type holding it by value, to a generic function or method as
// the type argument of a comparable type parameter would recurse forever,
// since == on that parameter calls Equal. The call is rejected. The same
// helper on a field, on another type, outside Equal, or over an `any`
// parameter is accepted.
func TestEqualReentry(t *testing.T) {
	const decls = `package main

func same[T comparable](a T, b T) bool = a == b

func either[T any](a T, b T) bool = true

struct Util(n int)

func (u Util) Same[T comparable](a T, b T) bool = a == b

struct Other(n int)

struct Pair[T comparable](a T, b T)

func (p Pair[T]) Same() bool = p.a == p.b

struct Point(x int, label string)
`
	rejected := []struct {
		name string
		body string
		arg  string
	}{
		{"its own type", `func (p Point) Equal(o Point) bool = same(p, o)`, "Point"},
		{"after a field comparison", `func (p Point) Equal(o Point) bool = same(p.label, o.label) && same(p, o)`, "Point"},
		{"a written type argument", `func (p Point) Equal(o Point) bool = same[Point](p, o)`, "Point"},
		{"named arguments", `func (p Point) Equal(o Point) bool = same(b = o, a = p)`, "Point"},
		{"an Option holding it", `func (p Point) Equal(o Point) bool = same(Some(p), Some(o))`, "Option[Point]"},
		{"a generic method", `func (p Point) Equal(o Point) bool = Util(0).Same(p, o)`, "Point"},
		{"inside a lambda", `func (p Point) Equal(o Point) bool {
    val f = (a Point) => same(a, o)
    f(p)
}`, "Point"},
		// A method of a generic type is compiled with the type's comparable
		// type parameter, as a generic function is.
		{"a method of a comparable generic type", `func (p Point) Equal(o Point) bool = Pair(p, o).Same()`, "Point"},
		{"a method of a comparable generic type, on an Option", `func (p Point) Equal(o Point) bool = Pair(Some(p), Some(o)).Same()`, "Option[Point]"},
		// A function or method of the file that Equal calls is followed.
		{"a helper", `func eqPoints(a Point, b Point) bool = same(a, b)

func (p Point) Equal(o Point) bool = eqPoints(p, o)`, "Point"},
		{"a helper declared after Equal", `func (p Point) Equal(o Point) bool = eqPoints(p, o)

func eqPoints(a Point, b Point) bool = same(a, b)`, "Point"},
		{"a helper method", `func (p Point) sameAs(o Point) bool = same(p, o)

func (p Point) Equal(o Point) bool = p.sameAs(o)`, "Point"},
		{"a helper calling a comparable generic type's method", `func both(a Point, b Point) bool = Pair(a, b).Same()

func (p Point) Equal(o Point) bool = both(p, o)`, "Point"},
		{"a helper calling a generic method", `func viaUtil(a Point, b Point) bool = Util(0).Same(a, b)

func (p Point) Equal(o Point) bool = viaUtil(p, o)`, "Point"},
		{"two helpers deep, one recursive", `func outer(a Point, b Point, n int) bool = if (n > 0) outer(a, b, n - 1) else inner(a, b)

func inner(a Point, b Point) bool = same(a, b)

func (p Point) Equal(o Point) bool = p.x == o.x && outer(p, o, 3)`, "Point"},
		{"a helper called with no arguments", `val origin = Point(0, "")

func originSame() bool = same(origin, origin)

func (p Point) Equal(o Point) bool = p.x == o.x && originSame()`, "Point"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+tc.body+"\n\nfunc main() = Println(Point(1, \"a\") == Point(2, \"a\"))\n")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeEqualReentry))
			require.Contains(t, err.Error(), "Point's own Equal calls")
			require.Contains(t, err.Error(), "with T = "+tc.arg+", a comparable type parameter")
		})
	}

	accepted := []struct {
		name string
		body string
	}{
		{"fields only", `func (p Point) Equal(o Point) bool = same(p.x, o.x) && same(p.label, o.label)`},
		{"another type", `func (p Point) Equal(o Point) bool = same(Other(p.x), Other(o.x))`},
		{"an any type parameter", `func (p Point) Equal(o Point) bool = either(p, o) && p.x == o.x`},
		{"outside Equal", `func (p Point) Same(o Point) bool = same(p, o)`},
		{"a holder whose own Equal compares fields", `struct Wrapper(p Point)

func (w Wrapper) Equal(o Wrapper) bool = w.p.x == o.p.x

func (p Point) Equal(o Point) bool = same(Wrapper(p), Wrapper(o))`},
		{"an Equal == does not call", `func (p Point) Equal(o Other) bool = same(p, p)`},
		{"a method of a comparable generic type at a field's type", `func (p Point) Equal(o Point) bool = Pair(p.x, o.x).Same() && Pair(p.label, o.label).Same()`},
		{"a helper comparing fields", `func eqPoints(a Point, b Point) bool = same(a.x, b.x) && same(a.label, b.label)

func (p Point) Equal(o Point) bool = eqPoints(p, o)`},
		{"a recursive helper comparing fields", `func eqFrom(a Point, b Point, n int) bool = if (n > 0) eqFrom(a, b, n - 1) else same(a.x, b.x)

func (p Point) Equal(o Point) bool = eqFrom(p, o, 2)`},
		{"a helper Equal does not call", `func eqPoints(a Point, b Point) bool = same(a, b)

func (p Point) Equal(o Point) bool = p.x == o.x`},
		{"a generic helper whose own type parameter reaches the comparer", `func viaAny[U comparable](a U, b U) bool = same(a, b)

func (p Point) Equal(o Point) bool = viaAny(p.x, o.x)`},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+tc.body+"\n\nfunc main() = Println(Point(1, \"a\"))\n")
			require.NoError(t, err)
		})
	}

	// A helper method of the same generic type instantiates the comparer
	// with the receiver's own type parameters, which Equal binds as well.
	t.Run("rejects a helper method of the same generic type", func(t *testing.T) {
		_, err := transpileBareVariant(t, `package main

func same[T comparable](a T, b T) bool = a == b

struct Box[T comparable](v T)

func (b Box[T]) sameAs(o Box[T]) bool = same(b, o)

func (b Box[T]) Equal(o Box[T]) bool = b.sameAs(o)

func main() = Println(Box(1) == Box(1))
`)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Box's own Equal calls Box.sameAs, which calls same with T = Box[T]")
	})

	// A library package keys its types under its name; the walk and the
	// message see them as the source names them.
	t.Run("rejects a helper in a library package", func(t *testing.T) {
		_, err := transpileBareVariant(t, `package geo

func same[T comparable](a T, b T) bool = a == b

struct Point(x int, label string)

func (p Point) sameAs(o Point) bool = same(p, o)

func (p Point) Equal(o Point) bool = p.sameAs(o)
`)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Point's own Equal calls Point.sameAs, which calls same with T = ")
	})

	// A receiver's `_` is named internally; the diagnostic spells it `_`.
	t.Run("spells a receiver's _ as written", func(t *testing.T) {
		_, err := transpileBareVariant(t, `package main

type Seq[T any, C any] interface {
    Rest() C
    Show() string
}

func same[T comparable](a T, b T) bool = a == b

struct Holder[T any, C Seq[T, C]](inner C)

func (h Holder[_, C]) Equal(o Holder[_, C]) bool = same(h, o)

func main() = Println("x")
`)
		require.Error(t, err)
		require.Contains(t, err.Error(), "with T = Holder[_, C], a comparable type parameter")
		require.NotContains(t, err.Error(), "_gala_")
	})
}
