package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interfaceImplDecls declares the interfaces and types the cases below use.
// Each concrete type misses its interface by one method: Tags returns its own
// type where Seq declares Seq[T] (no covariant results), Wide takes a string
// where Sink takes an int, Counter has Inc on a pointer receiver, Mute has no
// Speak, and Plain, a block-form struct with no generated String, has no
// String.
const interfaceImplDecls = `package main

import "fmt"
import . "martianoff/gala/collection_immutable"

type Seq[T any] interface {
    Size() int
    Tail() Seq[T]
}

struct Tags[T any](n int)

func (s Tags[T]) Size() int = s.n
func (s Tags[T]) Tail() Tags[T] = s

type Sink interface {
    Put(n int)
}

struct Wide(id int)

func (w Wide) Put(s string) {}

type Incer interface {
    Inc()
}

struct Counter(n int)

func (c *Counter) Inc() {}

type Speaker interface {
    Speak() string
}

struct Mute(id int)

// A block-form struct has no generated String.
type Plain struct {
    ID int
}

struct Holder(s Speaker)

func size(s Seq[int]) int = s.Size()
func put(s Sink) {}
func inc(i Incer) {}
func speak(s Speaker) string = s.Speak()
func show(s fmt.Stringer) string = s.String()
func count(xs Array[Speaker]) int = xs.Size()
`

// TestInterfaceNotImplemented: a value of a concrete type that does not
// implement the interface of the slot it fills is GALA-E0100 at every kind of
// slot, naming the first method that fails and why.
func TestInterfaceNotImplemented(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name:     "argument, covariant result",
			body:     "func main() = Println(size(Tags[int](1)))",
			contains: "Tags[int] does not implement Seq[int]: method Tail returns Tags[int], not Seq[int]",
		},
		{
			name:     "argument, wrong parameter type",
			body:     "func main() = put(Wide(1))",
			contains: "Wide does not implement Sink: method Put takes (string), not (int)",
		},
		{
			name:     "argument, pointer receiver",
			body:     "func main() = inc(Counter(1))",
			contains: "Counter does not implement Incer: method Inc has a pointer receiver",
		},
		{
			name:     "argument, missing method",
			body:     "func main() = Println(speak(Mute(1)))",
			contains: "Mute does not implement Speaker: it has no method Speak",
		},
		{
			name: "declared val",
			body: `func main() {
    val s Speaker = Mute(1)
    Println(s)
}`,
			contains: "Mute does not implement Speaker: it has no method Speak",
		},
		{
			name:     "result",
			body: `func asSpeaker() Speaker = Mute(1)

func main() = Println(asSpeaker())`,
			contains: "Mute does not implement Speaker: it has no method Speak",
		},
		{
			name:     "struct field",
			body:     "func main() = Println(Holder(Mute(1)))",
			contains: "Mute does not implement Speaker: it has no method Speak",
		},
		{
			name:     "collection element",
			body:     "func main() = Println(count(ArrayOf[Speaker](Mute(1))))",
			contains: "Mute does not implement Speaker: it has no method Speak",
		},
		{
			name: "assignment",
			body: `func main() {
    var s Speaker = nil
    s = Mute(1)
    Println(s)
}`,
			contains: "Mute does not implement Speaker: it has no method Speak",
		},
		{
			name: "missing method named like one generated on other kinds of type",
			body: `type Ranked interface {
    Compare(other Mute) int
}

func rank(r Ranked) {}

func main() = rank(Mute(1))`,
			contains: "Mute does not implement Ranked: it has no method Compare",
		},
		{
			name: "std collection as Iterable of another self type",
			body: `func main() {
    val it Iterable[int, List[int]] = ArrayOf(1, 2)
    Println(it.Size())
}`,
			contains: "collection_immutable.Array[int] does not implement Iterable[int, List[int]]: method Tail returns collection_immutable.Array[int], not List[int]",
		},
		{
			name: "error",
			body: `func fail() error = Mute(1)

func main() = Println(fail())`,
			contains: "Mute does not implement error: it has no method Error",
		},
		{
			name: "Go interface",
			body: `func main() = Println(show(Plain(ID = 1)))`,
			contains: "Plain does not implement fmt.Stringer: it has no method String",
		},
		{
			name: "Go interface, wrong result",
			body: `func (p Plain) String() int = 1

func main() = Println(show(Plain(ID = 1)))`,
			contains: "Plain does not implement fmt.Stringer: method String returns int, not string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newInterfaceImplTranspiler().Transpile(interfaceImplDecls+"\n"+tc.body+"\n", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeInterfaceNotImplemented))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}

// TestInterfaceImplementedAccepted: types that implement their interface,
// as Go checks it, are accepted at every kind of slot, and the generated Go
// type-checks (the oracle runs over every program transpiled here).
func TestInterfaceImplementedAccepted(t *testing.T) {
	input := `package main

import "fmt"
import . "martianoff/gala/collection_immutable"

type Seq[T any] interface {
    Size() int
    Tail() Seq[T]
    Each(f func(T))
}

struct Box[T any](n int)

func (b Box[T]) Size() int = b.n
func (b Box[T]) Tail() Seq[T] = b
func (b Box[T]) Each(f func(T)) {}

// A method may name the receiver's type parameters its own way.
struct Pair[A any](n int)

func (p Pair[X]) Size() int = p.n
func (p Pair[X]) Tail() Seq[X] = p
func (p Pair[X]) Each(f func(X)) {}

type Incer interface {
    Inc()
}

struct Counter(n int)

func (c *Counter) Inc() {}

type Shape interface {
    Area() float64
    Name() string
}

sealed type Figure {
    case Circle(R float64)
    case Square(S float64)
}

func (f Figure) Area() float64 = f match {
    case Circle(r) => r * r * 3.0
    case Square(s) => s * s
}

func (f Figure) Name() string = "figure"

type Equaler interface {
    Equal(other Point) bool
}

struct Point(X int, Y int)

struct Holder(s Shape)

type Fault struct {
    Msg string
}

func (f Fault) Error() string = f.Msg

func (p Point) String() string = "point"

// A method declared on an alias is the aliased type's.
type Summer interface {
    Sum() int
}

type Coord Point

func (c Coord) Sum() int = c.X + c.Y

func sum(s Summer) int = s.Sum()

// A shorthand struct implements fmt.Stringer through its generated String.
struct Tag(n int)

func size(s Seq[int]) int = s.Size()
func inc(i Incer) {}
func area(s Shape) float64 = s.Area()
func eq(e Equaler) bool = e.Equal(Point(1, 2))
func show(s fmt.Stringer) string = s.String()
func fail() error = Fault("bad")
func asShape() Shape = Circle(1.0)
func sizeOf[T any](b Box[T]) int {
    val s Seq[T] = b
    s.Size()
}

// A std collection implements Iterable with itself as the self type.
func evens[C any](xs Iterable[int, C]) C = xs.Filter((x) => x % 2 == 0)

func main() {
    val it Iterable[int, Array[int]] = ArrayOf(1, 2, 3)
    val ls Iterable[string, List[string]] = ListOf("a")
    Println(it.Tail().Get(0), ls.Size(), evens(ArrayOf(1, 2)).Get(0), evens(ListOf(3, 4)).Head())
    var c = Counter(1)
    inc(&c)
    val s Shape = Square(2.0)
    var t Shape = Circle(1.0)
    t = s
    Println(size(Box[int](1)), area(Circle(1.0)), eq(Point(1, 2)), show(Point(1, 2)), fail(), asShape().Name(), t.Name())
    Println(area(Holder(Square(1.0)).s), size(Pair[int](2)), sum(Point(1, 2)), show(Tag(1)), ArrayOf[Shape](Circle(1.0), Square(1.0)), sizeOf(Box[string](0)))
}
`
	_, err := newInterfaceImplTranspiler().Transpile(input, "")
	require.NoError(t, err)
}

func newInterfaceImplTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	return newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
}
