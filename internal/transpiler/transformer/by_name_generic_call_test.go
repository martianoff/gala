package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestByNameGenericCall pins a call passed by name (to a `func() R`
// parameter): it is lowered against R, so a type argument only the slot fixes
// is R's and the call is the thunk's value (#1465, #1471). A call whose value
// is a function is still passed as it is. examples/by_name_generic_call.gala
// runs them.
func TestByNameGenericCall(t *testing.T) {
	const decls = `package main

struct Cell[T any](V T)

func (c Cell[T]) Or[U any](a U, b func() U) U = b()

func zero[V any]() V {
    var z V
    z
}

func run(d func() int) int = d()

func three() int = 3

func mk() func() int = three

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name:     "slot-typed generic call is the thunk's value",
			main:     `Println(Cell(0).Or(0, zero()))`,
			contains: "func() int {\n\t\treturn zero[int]()\n\t}",
		},
		{
			name: "generic call takes the slot's result type",
			main: `val x64 int64 = 3
    Println(Cell(0).Or(x64, Cell(0).Or(1, 2)))`,
			contains: "Cell_Or[int64, int](Cell[int]{V: std.NewImmutable(0)}, 1, func() int64 {",
		},
		{
			name:     "parenthesized call",
			main:     `Println(Cell(0).Or(0, (zero())))`,
			contains: "return (zero[int]())",
		},
		{
			name:     "call returning a function is passed as it is",
			main:     `Println(run(mk()))`,
			contains: "run(mk())",
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
