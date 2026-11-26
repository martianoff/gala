package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestByNameUntypedConst pins an untyped constant passed by name (a
// `func() U` parameter): its thunk binds U as the bare constant does in a `U`
// parameter, so the slot the call fills gives U over the constant's default
// type, and the thunk's result is then spelled at that type (#1331).
// examples/by_name_untyped_const.gala runs them.
func TestByNameUntypedConst(t *testing.T) {
	const decls = `package main

struct Box[T any](V T)

func (b Box[T]) Lazy[U any](d func() U, on bool) Option[U] = if (on) Some(d()) else None()

func lazy[U any](d func() U, on bool) Option[U] = if (on) Some(d()) else None()

func (b Box[T]) LazyOr[U any](d func() U, dflt U) U = d()

func runS[U any](f Sendable[func() U]) Option[U] = Some(f())

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name: "generic function, int64 slot",
			main: `val p Option[int64] = lazy(5, true)
    Println(p)`,
			contains: "lazy[int64](func() int64 {\n\t\treturn 5\n\t}, true)",
		},
		{
			name: "shift constant, float64 slot",
			main: `val p Option[float64] = lazy(1 << 3, true)
    Println(p)`,
			contains: "lazy[float64](func() float64 {\n\t\treturn 1 << 3\n\t}, true)",
		},
		{
			name: "Sendable thunk",
			main: `val s Option[int64] = runS(5)
    Println(s)`,
			contains: "runS[int64](func() int64 {\n\t\treturn 5\n\t})",
		},
		{
			name: "companion Apply",
			main: `val tr Try[int64] = Try(5)
    Println(tr)`,
			contains: "std.Try[int64]{}.Apply(func() int64 {\n\t\treturn 5\n\t})",
		},
		{
			name: "no slot keeps the default type",
			main: `val p = lazy(5, true)
    Println(p)`,
			contains: "lazy(func() int {\n\t\treturn 5\n\t}, true)",
		},
		{
			name: "method with type parameters of its own",
			main: `val m Option[int64] = Box(0).Lazy(5, true)
    Println(m)`,
			contains: "Box_Lazy[int64, int](Box[int]{V: std.NewImmutable(0)}, func() int64 {\n\t\treturn 5\n\t}, true)",
		},
		{
			name: "typed argument after the constant gives U",
			main: `val x64 int64 = 3
    Println(Box(0).LazyOr(5, x64))`,
			contains: "func() int64 {\n\t\treturn 5\n\t}, x64)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := newThunkSugarTranspiler().Transpile(decls+"func main() {\n    "+tt.main+"\n}", "")
			require.NoError(t, err)
			assert.Contains(t, mainBodyOf(t, out), tt.contains)
		})
	}
}
