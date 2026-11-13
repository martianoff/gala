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
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+tc.body+"\n\nfunc main() = Println(Point(1, \"a\"))\n")
			require.NoError(t, err)
		})
	}
}
