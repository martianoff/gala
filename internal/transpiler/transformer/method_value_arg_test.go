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

// TestMethodValueAsGenericMethodArg: a method value (`p.Parse`, a method named
// without being called) passed to a generic method types that method's own
// type parameter. It used to have no type at all, so `in.FlatMap(p.Parse)`
// left FlatMap's result as Try[__mtp0] and the next lambda in the chain was
// emitted with the internal sentinel as its parameter type.
func TestMethodValueAsGenericMethodArg(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "method of a plain struct",
			input: `package main

struct Parser(Base int)

func (p Parser) Parse(s string) Try[int] = Success(s.Size() + p.Base)

func main() {
    val p = Parser(10)
    val in Try[string] = Success("five")
    Println(in.FlatMap(p.Parse).Map(_ * 2))
}`,
			want: "func(__p0 int) int",
		},
		{
			name: "method of a generic struct takes the receiver's type arguments",
			input: `package main

struct Box[T any](Value T)

func (b Box[T]) Wrap(s string) Option[T] = if (s == "") None() else Some(b.Value)

func main() {
    val b = Box(3)
    val in Option[string] = Some("x")
    Println(in.FlatMap(b.Wrap).Map(_ + 1))
}`,
			want: "func(__p0 int) int",
		},
		{
			name: "alias that reorders its target's arguments",
			input: `package main

struct Pair[A any, B any](First A, Second B)

func (p Pair[A, B]) Pick(k int) Option[B] = if (k > 0) Some(p.Second) else None()

type Flip[A any, B any] Pair[B, A]

func main() {
    val f Flip[int, string] = Pair("s", 1)
    Println(Some(1).FlatMap(f.Pick).Map(_ + 1))
}`,
			want: "func(__p0 int) int",
		},
		{
			name: "method declared on an alias",
			input: `package main

struct Point(X int, Y int)

type Coord Point

func (c Coord) Shift(n int) Option[int] = Some(c.X + n)

func main() {
    val c Coord = Coord(1, 2)
    Println(Some(1).FlatMap(c.Shift).Map(_ * 2))
}`,
			want: "func(__p0 int) int",
		},
		{
			name: "codec Decode passed to FlatMap",
			input: `package main

import "martianoff/gala/json"

struct P(N int)

func main() {
    val c = json.Codec[P](json.SnakeCase())
    Println(c.Encode(P(1)).FlatMap(c.Decode).Map(_.N))
}`,
			want: "func(__p0 P) int",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "method_value.gala")
			require.NoError(t, err)
			assert.NotContains(t, got, "__mtp")
			assert.Contains(t, got, tt.want)
		})
	}
}
