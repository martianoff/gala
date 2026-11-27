package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenericFuncWeakConstByName pins a generic function's argument passed by
// name: it binds the type parameter as its thunk's value, so a typed value
// replaces a binding only an untyped constant made and a wider constant widens
// it (#1470). examples/generic_func_weak_const_by_name.gala runs them.
func TestGenericFuncWeakConstByName(t *testing.T) {
	const decls = `package main

func or[U any](a U, b func() U) U = b()

func both[U any](d func() U, e func() U) U = e()

func pick[U any](d func() U, v U) U = d()

func orO[U any](a U, b func() U) Option[U] = Some(b())

type Shape interface {
    Area() int
}

struct Sq(N int)

func (s Sq) Area() int = s.N * s.N

`
	tests := []struct {
		name     string
		main     string
		contains string
	}{
		{
			name: "typed value after a bare constant",
			main: `val x64 int64 = 3
    Println(or(0, x64))`,
			contains: "or(0, func() int64 {\n\t\treturn x64\n\t})",
		},
		{
			name: "typed value after a by-name constant",
			main: `val x64 int64 = 3
    Println(both(5, x64))`,
			contains: "both(func() int64 {\n\t\treturn 5\n\t}, func() int64 {\n\t\treturn x64\n\t})",
		},
		{
			name:     "wider constant widens",
			main:     `Println(both(5, 2.5))`,
			contains: "both(func() float64 {\n\t\treturn 5\n\t}, func() float64 {\n\t\treturn 2.5\n\t})",
		},
		{
			name: "bare typed argument binds before a by-name one",
			main: `val sh Shape = Sq(2)
    Println(pick(Sq(3), sh).Area())`,
			contains: "pick(func() Shape {",
		},
		{
			name: "Sendable function value is the thunk",
			main: `val f Sendable[func() int] = () => 7
    Println(or(0, f))`,
			contains: "or(0, f)",
		},
		{
			name: "the call's slot still fixes U",
			main: `val n = 4
    val r Option[any] = orO(0, n)
    Println(r)`,
			contains: "orO(0, func() any {",
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
