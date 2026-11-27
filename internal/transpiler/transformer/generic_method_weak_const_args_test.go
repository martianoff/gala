package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenericMethodWeakConstArgs pins a generic method's type parameter that
// an untyped constant binds only weakly: a later argument passed by name is
// lowered with the binding open, so its own type replaces or widens it
// (#1444, #1423), and the slot is spelled when any constant's default type
// differs from it, not only the last one's (#1423).
// examples/generic_method_weak_const_args.gala runs them.
func TestGenericMethodWeakConstArgs(t *testing.T) {
	const decls = `package main

struct Cell[T any](V T)

func (c Cell[T]) Both[U any](d func() U, e func() U) U = e()

func (c Cell[T]) Or[U any](a U, b func() U) U = b()

func (c Cell[T]) Pick[U any](a U, b U) U = a

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name: "typed by-name argument after a by-name constant",
			main: `val x64 int64 = 3
    Println(Cell(0).Both(5, x64))`,
			contains: "func() int64 {\n\t\treturn 5\n\t}, func() int64 {\n\t\treturn x64\n\t})",
		},
		{
			name:     "wider by-name constant widens",
			main:     `Println(Cell(0).Both(5, 2.5))`,
			contains: "func() float64 {\n\t\treturn 5\n\t}, func() float64 {\n\t\treturn 2.5\n\t})",
		},
		{
			name:     "by-name constant after a bare one",
			main:     `Println(Cell(0).Or(0, 2.5))`,
			contains: "0, func() float64 {\n\t\treturn 2.5\n\t})",
		},
		{
			name: "typed operator expression after a by-name constant",
			main: `val x64 int64 = 3
    Println(Cell(0).Both(5, x64 + 1))`,
			contains: "func() int64 {\n\t\treturn 5\n\t}, func() int64 {\n\t\treturn x64 + 1\n\t})",
		},
		{
			name:     "narrower by-name constant after a wider bare one",
			main:     `Println(Cell(0).Or(2.5, 0))`,
			contains: "2.5, func() float64 {\n\t\treturn 0\n\t})",
		},
		{
			name: "slot spelled when an earlier constant differs from it",
			main: `val n int = Cell(0).Pick(1.0, 2)
    Println(n)`,
			contains: "Cell_Pick[int, int](",
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
