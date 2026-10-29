package transformer_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

const equalityHeader = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n"

// equalCall matches a call of an Equal method in generated Go.
var equalCall = regexp.MustCompile(`\.Equal\(`)

// synthesizedEqual matches the lines of a synthesized Equal method, which
// call std.Equal on each field, and std.Equal calls written in the source.
var synthesizedEqual = regexp.MustCompile(`std\.Equal\(|^func \(`)

// TestEqualityLowersToEqual: `==` and `!=` compare values by their Equal
// method, which every GALA struct, sealed type and tuple has and the
// collections declare: `a == b` is `a.Equal(b)` and `a != b` is
// `!a.Equal(b)`. A stable-identifier pattern compares the same way. Each
// case's generated Go holds every want substring.
func TestEqualityLowersToEqual(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{name: "List", src: `func main() { Println(ListOf(1, 2) == ListOf(1, 2), ListOf(1) != ListOf(2)) }`,
			want: []string{"ListOf(1, 2).Equal(ListOf(1, 2))", "!ListOf(1).Equal(ListOf(2))"}},
		{name: "List vals and parameters", src: `func differ(a List[int], b List[int]) bool = a != b

func main() {
    val a = ListOf(1)
    val b = ListOf(1)
    Println(a == b, differ(a, b))
}`, want: []string{"return !a.Equal(b)", "a.Equal(b), differ"}},
		{name: "Array", src: `func same(a Array[int], b Array[int]) bool = a == b

func main() { Println(same(ArrayOf(1), ArrayOf(1)), ArrayOf(1) != ArrayOf(2)) }`,
			want: []string{"return a.Equal(b)", "!ArrayOf(1).Equal(ArrayOf(2))"}},
		{name: "HashMap, HashSet, TreeSet and TreeMap", src: `func main() {
    val m = HashMapOf(("a", 1))
    val s = HashSetOf(1)
    val t = TreeSetOf(1)
    val u = TreeMapOf((1, "a"))
    Println(m == HashMapOf(("a", 1)), s != HashSetOf(1), t == TreeSetOf(1), u == TreeMapOf((1, "a")))
}`, want: []string{"m.Equal(HashMapOf(", "!s.Equal(HashSetOf(1))", "t.Equal(TreeSetOf(1))", "u.Equal(TreeMapOf("}},
		{name: "mutable Array by value", src: `import "martianoff/gala/collection_mutable"

func same(a collection_mutable.Array[int], b collection_mutable.Array[int]) bool = a == b

func main() {
    val a = collection_mutable.ArrayOf(1)
    Println(same(*a, *a))
}`, want: []string{"return a.Equal(b)"}},
		{name: "struct holding a List", src: `struct Post(title string, tags List[string])

func main() { Println(Post("x", ListOf("t")) == Post("x", ListOf("t"))) }`, want: []string{"}.Equal(Post{"}},
		{name: "struct holding an Array, not equal", src: `struct P(name string, tags Array[string])

func main() {
    val a = P("x", ArrayOf("t"))
    val b = P("y", ArrayOf("t"))
    Println(a != b)
}`, want: []string{"!a.Equal(b)"}},
		{name: "struct with a pointer field", src: `struct Node(v int, next *Node)

func main() { Println(Node(1, nil) == Node(1, nil)) }`, want: []string{"}.Equal(Node{"}},
		{name: "struct with a function field", src: `struct H(f func(int) int)

func inc(x int) int = x + 1

func main() { Println(H(inc) == H(inc)) }`, want: []string{"}.Equal(H{"}},
		{name: "struct with an interface field", src: `struct A(v any)

func main() { Println(A(1) == A(1)) }`, want: []string{"}.Equal(A{"}},
		{name: "struct with a Go struct field", src: `import "time"

struct At(t time.Time)

func main() { Println(At(time.Unix(0, 0)) == At(time.Unix(0, 0))) }`, want: []string{"}.Equal(At{"}},
		{name: "tuple holding a List", src: `func main() { Println((1, ListOf("t")) == (1, ListOf("t"))) }`, want: []string{"}.Equal(std.Tuple[int, List[string]]{"}},
		{name: "Option of a List", src: `func differ(a Option[List[int]], b Option[List[int]]) bool = a != b

func main() { Println(differ(Some(ListOf(1)), None[List[int]]())) }`, want: []string{"return !a.Equal(b)"}},
		{name: "sealed type with a case holding a List", src: `sealed type Path {
    case Poly(points List[int])
    case Dot(x int)
}

func main() { Println(Poly(ListOf(1)) == Dot(1)) }`, want: []string{"Poly{}.Apply(ListOf(1)).Equal(Dot{}.Apply(1))"}},
		{name: "recursive sealed type", src: `sealed type Tree {
    case Leaf(v int)
    case Fork(l Tree, r Tree)
}

func main() { Println(Fork(Leaf(1), Leaf(2)) == Fork(Leaf(1), Leaf(2))) }`, want: []string{").Equal(Fork{}.Apply("}},
		{name: "generic struct over a type parameter of the enclosing function", src: `struct Box[T any](v T)

func same[T any](a T, b T) bool = Box(a) == Box(b)

func main() { Println(same(ListOf(1), ListOf(1))) }`, want: []string{"}.Equal(Box[T]{"}},
		{name: "struct over a comparable type parameter of the enclosing function", src: `struct Box[T comparable](v T)

func same[T comparable](a T, b T) bool = Box(a) == Box(b)

func main() { Println(same(1, 1)) }`, want: []string{"}.Equal(Box[T]{"}},
		{name: "type with its own Equal", src: `struct Cell(v int)

func (c Cell) Equal(o Cell) bool = c.v % 10 == o.v % 10

func main() { Println(Cell(1) == Cell(11)) }`, want: []string{"}.Equal(Cell{"}},
		{name: "struct holding a type with its own Equal", src: `struct Cell(v int)

func (c Cell) Equal(o Cell) bool = c.v % 10 == o.v % 10

struct Pair(a Cell, b Cell)

func main() { Println(Pair(Cell(1), Cell(2)) == Pair(Cell(11), Cell(12))) }`, want: []string{"}.Equal(Pair{"}},
		{name: "Equal with a pointer receiver", src: `struct Cell(v int)

func (c *Cell) Equal(o Cell) bool = c.v == o.v

func main() { Println(Cell(1) == Cell(1)) }`, want: []string{"std.AddrOfCopy(Cell{"}},
		{name: "stable identifier holding a List", src: `val Answer = ListOf(42)

func describe(xs List[int]) string = xs match {
    case Answer => "answer"
    case _      => "other"
}

func main() { Println(describe(ListOf(42))) }`, want: []string{".Equal(Answer.Get())"}},
		{name: "stable identifier holding a struct with a List", src: `struct Post(tags List[string])

val Intro = Post(ListOf("intro"))

func describe(p Post) string = p match {
    case Intro => "intro"
    case _     => "other"
}

func main() { Println(describe(Post(ListOf("intro")))) }`, want: []string{".Equal(Intro.Get())"}},
		{name: "a field compared inside another type's Equal", src: `struct W(xs List[int])

func (w W) Equal(o W) bool = w.xs == o.xs

func main() { Println(W(ListOf(1)) == W(ListOf(1))) }`, want: []string{"w.xs.Get().Equal(o.xs.Get())", "}.Equal(W{"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newDocGuardTranspiler().Transpile(equalityHeader+tc.src, "main.gala")
			require.NoError(t, err)
			for _, w := range tc.want {
				assert.Contains(t, out, w)
			}
		})
	}
}

// TestEqualityKeepsGoEquality: `==` stays Go's where that gives Equal's
// answer — a type of primitives only, through fields, cases and type
// arguments, with no Equal of its own — and, with Go's meaning, where there is
// no Equal: pointers, interfaces, nil, type parameters and Go types. Inside a
// type's own Equal, == on that type is Go's, or the method would call itself.
// No case's generated Go calls an Equal method outside the synthesized ones.
func TestEqualityKeepsGoEquality(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // the comparison in the generated Go
	}{
		{name: "ints and strings", src: `func f(a int, b string) bool = a == 1 && b != "x"

func main() { Println(f(1, "y")) }`, want: `return a == 1 && b != "x"`},
		{name: "floats", src: `func f(a float64, b float64) bool = a == b

func main() { Println(f(1.0, 2.0)) }`, want: "return a == b"},
		{name: "struct of primitives", src: `struct Pt(x int, y string, z float64)

func same(a Pt, b Pt) bool = a == b

func main() { Println(same(Pt(1, "a", 0.5), Pt(1, "a", 0.5))) }`, want: "return a == b"},
		{name: "struct holding a struct of primitives", src: `struct Pt(x int, y int)

struct Seg(a Pt, b Pt)

func same(a Seg, b Seg) bool = a != b

func main() { Println(same(Seg(Pt(0, 0), Pt(1, 1)), Seg(Pt(0, 0), Pt(1, 1)))) }`, want: "return a != b"},
		{name: "struct holding a Go named primitive", src: `import "time"

struct Wait(d time.Duration)

func same(a Wait, b Wait) bool = a == b

func main() { Println(same(Wait(time.Second), Wait(time.Second))) }`, want: "return a == b"},
		{name: "Option of int", src: `func same(a Option[int], b Option[int]) bool = a == b

func main() { Println(same(Some(1), Some(1))) }`, want: "return a == b"},
		{name: "tuple of primitives", src: `func same(a Tuple[int, string], b Tuple[int, string]) bool = a == b

func main() { Println(same((1, "a"), (1, "a"))) }`, want: "return a == b"},
		{name: "sealed type of primitives", src: `sealed type Shape {
    case Circle(r int)
    case Square(side int)
}

func same(a Shape, b Shape) bool = a == b

func main() { Println(same(Circle(1), Square(1))) }`, want: "return a == b"},
		{name: "generic struct instantiated with a primitive", src: `struct Box[T any](v T)

func same(a Box[int], b Box[int]) bool = a == b

func main() { Println(same(Box(1), Box(1))) }`, want: "return a == b"},
		{name: "pointers", src: `struct Node(v int)

func same(a *Node, b *Node) bool = a == b

func main() { Println(same(nil, nil)) }`, want: "return a == b"},
		{name: "interface values", src: `func same(a any, b any) bool = a == b

func main() { Println(same(1, ArrayOf(1))) }`, want: "return a == b"},
		{name: "type parameter constrained by comparable", src: `func same[T comparable](a T, b T) bool = a == b

func main() { Println(same(1, 1)) }`, want: "return a == b"},
		{name: "comparisons with nil", src: `func f(g func(int) int, m map[string]int, s []int) bool = g == nil || m != nil || nil == s

func main() { Println(f(nil, nil, nil)) }`, want: "return g == nil || m != nil || nil == s"},
		{name: "Go comparable struct", src: `import "time"

func same(a time.Time, b time.Time) bool = a == b

func main() { Println(same(time.Unix(0, 0), time.Unix(0, 0))) }`, want: "return a == b"},
		{name: "mutable collections by pointer", src: `import "martianoff/gala/collection_mutable"

func same(a *collection_mutable.Array[int], b *collection_mutable.Array[int]) bool = a == b

func main() { Println(same(collection_mutable.ArrayOf(1), collection_mutable.ArrayOf(1))) }`, want: "return a == b"},
		{name: "own Equal compares its own type with Go's ==", src: `struct Cell(v int, next *Cell)

func (c Cell) Equal(o Cell) bool = c == o

func main() { Println(Equal(Cell(1, nil), Cell(1, nil))) }`, want: "return c == o"},
		{name: "stable identifier holding an int", src: `val Answer = 42

func describe(n int) string = n match {
    case Answer => "answer"
    case _      => "other"
}

func main() { Println(describe(42)) }`, want: "== Answer.Get()"},
		{name: "stable identifier holding a struct of primitives", src: `struct Pt(x int, y int)

val Origin = Pt(0, 0)

func describe(p Pt) string = p match {
    case Origin => "origin"
    case _      => "other"
}

func main() { Println(describe(Pt(0, 0))) }`, want: "== Origin.Get()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newDocGuardTranspiler().Transpile(equalityHeader+tc.src, "main.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
			for _, line := range strings.Split(out, "\n") {
				if equalCall.MatchString(line) && !synthesizedEqual.MatchString(line) {
					t.Errorf("unexpected Equal call: %s", line)
				}
			}
		})
	}
}

// TestIncomparableOperands: an operand Go cannot compare and that has no
// Equal — a function, a Go map, a Go type holding one, a type parameter
// constrained by any — is GALA-E0084 in GALA's terms; before, the comparison
// reached `go build` and failed there, naming the internal std.Immutable
// wrapper. So is == on a type Go cannot compare inside that type's own Equal.
func TestIncomparableOperands(t *testing.T) {
	cases := []struct {
		name string
		src  string
		msg  []string // substrings of the GALA-E0084 message and hint
	}{
		{
			name: "type parameter constrained by any",
			src: `func same[T any](a T, b T) bool = a == b

func main() { Println(same(1, 1)) }`,
			msg: []string{"== cannot compare T values: T is a type parameter constrained by any",
				"constrain T with comparable where it is declared (`T comparable`); or compare with `Equal(a, b)`"},
		},
		{
			name: "method comparing values of the receiver's type parameter",
			src: `struct Box[T any](v T)

func (b Box[T]) Holds(x T) bool = b.v == x

func main() { Println(Box(1).Holds(1)) }`,
			msg: []string{"== cannot compare T values: T is a type parameter constrained by any"},
		},
		{
			name: "function values",
			src: `func inc(x int) int = x + 1

func main() {
    val f = inc
    Println(f == f)
}`,
			msg: []string{"== cannot compare func(int) int values: func(int) int is a function", "a function value cannot be compared, only called"},
		},
		{
			name: "Go map",
			src: `func main() {
    var m map[string]int
    var n map[string]int
    Println(m != n)
}`,
			msg: []string{"!= cannot compare map[string]int values: map[string]int is a Go map", "compare with Equal: `!Equal(m, n)`"},
		},
		{
			name: "Go struct that is not comparable",
			src: `import "bytes"

func same(a bytes.Buffer, b bytes.Buffer) bool = a == b

func main() { Println(same(bytes.Buffer{}, bytes.Buffer{})) }`,
			msg: []string{"== cannot compare bytes.Buffer values: bytes.Buffer is a Go type that is not comparable", "compare with Equal: `Equal(a, b)`"},
		},
		{
			name: "own Equal on a type Go cannot compare",
			src: `struct Bag(items func() int)

func (b Bag) Equal(o Bag) bool = b == o

func main() { Println(Bag(() => 1).Equal(Bag(() => 1))) }`,
			msg: []string{"== inside Bag's own Equal is Go's ==, which cannot compare Bag values: field items has type func() int, a function",
				"compare the fields one by one"},
		},
		{
			name: "stable identifier holding a function",
			src: `func inc(x int) int = x + 1

val F = inc

func describe(f func(int) int) string = f match {
    case F => "inc"
    case _ => "other"
}

func main() { Println(describe(inc)) }`,
			msg: []string{"case F compares the matched value with F by ==, which cannot compare func(int) int values", "a function value cannot be compared"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := equalityHeader + tc.src
			_, err := newDocGuardTranspiler().Transpile(src, "main.gala")
			require.Error(t, err)
			text := galaerr.RenderRich(err, galaerr.Options{FallbackPath: "main.gala", FallbackSource: src})
			assert.Contains(t, text, string(galaerr.CodeIncomparableOperands))
			for _, m := range tc.msg {
				assert.Contains(t, text, m)
			}
			assert.NotContains(t, text, "Immutable")
		})
	}
}
