package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenericMethodVariadicResultType: the result type of a call to a variadic
// method with type parameters of its own takes them from every argument the
// variadic parameter receives, and from a spread one's element. Only the first
// argument used to bind them, so `c.Last(0, 2.5)` was typed int while Go
// inferred float64 (#1420).
func TestGenericMethodVariadicResultType(t *testing.T) {
	const decls = `package main

import "net"
import . "martianoff/gala/collection_immutable"
import . "martianoff/gala/go_interop"

struct C(N int)

func (c C) Last[U any](xs ...U) U = ArrayOf(xs...).Last()

func (c C) Tag[K any, U any](k K, xs ...U) Tuple[K, U] = (k, ArrayOf(xs...).Last())

func halves() []float64 = SliceOf(1.5, 2.5)

type Floats []float64

func floats() Floats = SliceOf(1.5, 6.5)

func ip() net.IP = net.ParseIP("1.2.3.4")

func lastOf[T any](xs ...T) T = ArrayOf(xs...).Last()

struct Box[T any](V T)

func (b Box[T]) Pick[U any](t T, xs ...U) Tuple[T, U] = (t, ArrayOf(xs...).Last())

`
	tests := []struct {
		name string
		main string
		want string
	}{
		{
			name: "trailing untyped constant",
			main: "val w = () => C(1).Last(0, 2.5)\n    Println(w())",
			want: "func() float64",
		},
		{
			name: "trailing typed argument",
			main: "val x float64 = 3.5\n    val w = () => C(1).Last(1, x)\n    Println(w())",
			want: "func() float64",
		},
		{
			name: "after a leading parameter",
			main: "val w = () => C(1).Tag(\"k\", 1, 2.5)\n    Println(w())",
			want: "func() std.Tuple[string, float64]",
		},
		{
			name: "spread argument",
			main: "val w = () => C(1).Last(halves()...)\n    Println(w())",
			want: "func() float64",
		},
		{
			name: "spread of a defined slice type",
			main: "val w = () => C(1).Last(floats()...)\n    Println(w())",
			want: "func() float64",
		},
		{
			name: "spread of a Go defined slice type",
			main: "val w = () => C(1).Last(ip()...)\n    Println(w())",
			want: "func() byte",
		},
		{
			name: "generic function, spread of a defined slice type",
			main: "val w = () => lastOf(floats()...)\n    Println(w())",
			want: "func() float64",
		},
		{
			name: "generic function, spread of a Go defined slice type",
			main: "val w = () => lastOf(ip()...)\n    Println(w())",
			want: "func() byte",
		},
		{
			name: "generic receiver, trailing argument",
			main: "val b = Box(\"s\")\n    val w = () => b.Pick(\"t\", 1, 2.5)\n    Println(w())",
			want: "func() std.Tuple[string, float64]",
		},
		{
			name: "generic receiver, spread after a leading parameter",
			main: "val b = Box(\"s\")\n    val w = () => b.Pick(\"u\", halves()...)\n    Println(w())",
			want: "func() std.Tuple[string, float64]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			got, err := trans.Transpile(decls+"func main() {\n    "+tt.main+"\n}\n", "variadic_method.gala")
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
