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

// TestGenericMethodValue: a method value of a method with type parameters of
// its own (#1300) lowers to a closure over the function the method is, with
// its type parameters from the written type arguments or from the function
// type of the slot it fills; with neither, it is GALA-E0067.
func TestGenericMethodValue(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	const decls = `package main

import . "martianoff/gala/collection_immutable"

struct Box[T any](V T)

func (b Box[T]) Pair[U any](u U) Tuple[T, U] = (b.V, u)

func (b Box[T]) Empty[U any](t T) Option[U] = None()

func names() List[string] = ListOf("a")

`
	tests := []struct {
		name    string
		decl    string // declarations of the case's own
		main    string
		want    string
		wantErr string
	}{
		{
			name: "argument of a generic method",
			main: "val b = Box(1)\n    Println(names().Map(b.Pair))",
			want: "return Box_Pair[string, int](__recv_",
		},
		{
			name: "typed val",
			main: "val b = Box(1)\n    val f func(string) Tuple[int, string] = b.Pair\n    Println(f(\"x\"))",
			want: "return Box_Pair[string, int](__recv_",
		},
		{
			name: "Sendable slot",
			decl: "func spawn(f Sendable[func(int) Option[string]]) Option[string] = f(1)\n\n",
			main: "val b = Box(1)\n    Println(spawn(b.Empty))",
			want: "return Box_Empty[string, int](__recv_",
		},
		{
			name:    "Sendable slot of a generic method over a var receiver",
			decl:    "func (b Box[T]) Run[U any](u U, f Sendable[func(T) Option[U]]) Option[U] = f(b.V)\n\n",
			main:    "var b = Box(1)\n    b = Box(2)\n    Println(b.Run(\"s\", b.Empty))",
			wantErr: "GALA-E0037",
		},
		{
			name: "type parameter only the result gives",
			main: "val b = Box(1)\n    val f func(int) Option[bool] = b.Empty\n    Println(f(2))",
			want: "return Box_Empty[bool, int](__recv_",
		},
		{
			name: "written type argument",
			main: "val b = Box(1)\n    val f = b.Pair[bool]\n    Println(f(true))",
			want: "return Box_Pair[bool, int](__recv_",
		},
		{
			name:    "nothing gives the type parameter",
			main:    "val b = Box(1)\n    val f = b.Pair\n    Println(f(true))",
			wantErr: "GALA-E0067",
		},
		{
			name:    "std collection method",
			main:    "val f = names().GroupBy\n    Println(f)",
			wantErr: "cannot infer type argument K of the method value List.GroupBy",
		},
		{
			name:    "function type of another arity",
			main:    "val b = Box(1)\n    val f func(int, int) Tuple[int, string] = b.Pair\n    Println(f(1, 2))",
			wantErr: "the method value Box.Pair takes 1 argument(s), but the function type it is used as takes 2",
		},
		{
			name:    "function type of another arity with the type arguments written",
			main:    "val b = Box(1)\n    val f func(int, int) Tuple[int, string] = b.Pair[string]\n    Println(f(1, 2))",
			wantErr: "the method value Box.Pair takes 1 argument(s), but the function type it is used as takes 2",
		},
		{
			name:    "too many type arguments",
			main:    "val b = Box(1)\n    val f = b.Pair[bool, int]\n    Println(f(true))",
			wantErr: "too many type arguments for the method value Box.Pair",
		},
		{
			name: "receiver type argument named like the method's own",
			decl: "func pairs[U any](b Box[U]) func(string) Tuple[U, string] = b.Pair\n\n",
			main: "Println(pairs(Box(1))(\"a\"))",
			want: "return Box_Pair[string, U](__recv_",
		},
		{
			name:    "callee's unbound type parameter named like the enclosing one",
			decl:    "func makes[U any](b Box[int], u U) int = ListOf(1).Map(b.Empty).Size()\n\n",
			main:    "Println(makes(Box(1), true))",
			wantErr: "GALA-E0067",
		},
		{
			name:    "generic function's unbound type parameter named like the enclosing one",
			decl:    "func apply[A any, B any](g func(A) B, a A) B = g(a)\n\nfunc leak[B any](b Box[int], x B) {\n    Println(apply[int](b.Empty, 3))\n}\n\n",
			main:    "leak(Box(1), true)",
			wantErr: "GALA-E0067",
		},
		{
			name: "receiver's type argument named like the callee's own",
			decl: "func pairsOf[U any](xs List[U], b Box[int]) List[Tuple[int, U]] = xs.Map(b.Pair)\n\n",
			main: "Println(pairsOf(ListOf(true), Box(1)))",
			want: "return Box_Pair[U, int](__recv_",
		},
		{
			name: "callee's result inferred apart from a receiver type argument of the same name",
			decl: "func firsts[U any](xs List[U], b Box[int]) List[int] {\n    val ys = xs.Map(b.Pair)\n    ys.Map((p) => p.V1)\n}\n\n",
			main: "Println(firsts(ListOf(true), Box(1)))",
			want: "func(p std.Tuple[int, U]) int",
		},
		{
			name: "receiver's type argument nesting the enclosing type parameter",
			decl: "func pairsOpt[T any](xs List[Option[T]], b Box[int]) List[Tuple[int, Option[T]]] = xs.Map(b.Pair)\n\n",
			main: "Println(pairsOpt(ListOf(Some(1)), Box(1)))",
			want: "return Box_Pair[std.Option[T], int](__recv_",
		},
		{
			name:    "generic struct's unbound type parameter named like the enclosing one",
			decl:    "struct Wrap[A any](F func(int) Option[A])\n\nfunc wraps[A any](b Box[int], a A) {\n    Println(Wrap(b.Empty))\n}\n\n",
			main:    "wraps(Box(1), true)",
			wantErr: "GALA-E0067",
		},
		{
			name: "field named like a generic method in a by-name slot",
			decl: "struct Cfg(Map int)\n\nfunc (b Box[T]) Lazy[U any](d func() U) U = d()\n\n",
			main: "val c = Cfg(3)\n    val b = Box(1)\n    Println(b.Lazy(c.Map))",
			want: "func() int {",
		},
		{
			name: "generic function's slot",
			decl: "func apply[A any, B any](g func(A) B, a A) B = g(a)\n\n",
			main: "val b = Box(1)\n    Println(apply(b.Pair[string], \"z\"))",
			want: "return Box_Pair[string, int](__recv_",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.decl+"func main() {\n    "+tt.main+"\n}", "method_value.gala")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, got, tt.want)
		})
	}
}
