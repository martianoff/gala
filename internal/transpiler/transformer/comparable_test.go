package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestIncomparableOperands: `==`, `!=` and a stable-identifier pattern compare
// with Go's `==`. An operand whose type Go cannot compare is GALA-E0084 in
// GALA's terms; before, the comparison reached `go build` and failed there,
// naming the internal std.Immutable wrapper. Comparisons Go compiles are
// accepted unchanged, and a type that is not known is left to Go.
func TestIncomparableOperands(t *testing.T) {
	const header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n"
	cases := []struct {
		name string
		src  string
		msg  []string // substrings of the GALA-E0084 message and hint; nil: accepted
	}{
		{
			name: "struct with an Array field",
			src: `struct P(name string, tags Array[string])

func main() { Println(P("x", ArrayOf("t")) == P("x", ArrayOf("t"))) }`,
			msg: []string{"== cannot compare P values: field tags has type Array[string], which holds a Go slice",
				"compare with Equal: `P(\"x\", ArrayOf(\"t\")).Equal(P(\"x\", ArrayOf(\"t\")))`"},
		},
		{
			name: "struct with an Array field, not equal",
			src: `struct P(name string, tags Array[string])

func main() {
    val a = P("x", ArrayOf("t"))
    val b = P("y", ArrayOf("t"))
    Println(a != b)
}`,
			msg: []string{"!= cannot compare P values: field tags has type Array[string]", "compare with Equal: `!a.Equal(b)`"},
		},
		{
			name: "tuple holding an Array",
			src:  `func main() { Println((1, ArrayOf("t")) == (1, ArrayOf("t"))) }`,
			msg:  []string{"== cannot compare Tuple[int, Array[string]] values: element 2 has type Array[string], which holds a Go slice", ".Equal("},
		},
		{
			name: "type parameter constrained by any",
			src: `func same[T any](a T, b T) bool = a == b

func main() { Println(same(1, 1)) }`,
			msg: []string{"== cannot compare T values: T is a type parameter constrained by any",
				"constrain T with comparable where it is declared (`T comparable`); or compare with `Equal(a, b)`"},
		},
		{
			name: "generic struct over a type parameter constrained by any",
			src: `struct Box[T any](v T)

func same[T any](a T, b T) bool = Box(a) == Box(b)

func main() { Println(same(1, 1)) }`,
			msg: []string{"== cannot compare Box[T] values: field v has type T, a type parameter constrained by any", "Box(a).Equal(Box(b))"},
		},
		{
			name: "method comparing values of the receiver's type parameter",
			src: `struct Box[T any](v T)

func (b Box[T]) Holds(x T) bool = b.v == x

func main() { Println(Box(1).Holds(1)) }`,
			msg: []string{"== cannot compare T values: T is a type parameter constrained by any"},
		},
		{
			name: "sealed type with a variant holding an Array",
			src: `sealed type Shape {
    case Poly(points Array[int])
    case Dot(x int)
}

func main() { Println(Poly(ArrayOf(1)) == Dot(1)) }`,
			msg: []string{"cannot compare Shape values: field points of case Poly has type Array[int], which holds a Go slice"},
		},
		{
			name: "bare Array",
			src:  `func main() { Println(ArrayOf(1) == ArrayOf(1)) }`,
			msg:  []string{"== cannot compare Array[int] values: Array[int] holds a Go slice", "ArrayOf(1).Equal(ArrayOf(1))"},
		},
		{
			name: "Array vals",
			src: `func main() {
    val a = ArrayOf("x")
    val b = ArrayOf("y")
    Println(a == b)
}`,
			msg: []string{"== cannot compare Array[string] values: Array[string] holds a Go slice", "compare with Equal: `a.Equal(b)`"},
		},
		{
			name: "Array vals, not equal",
			src: `func main() {
    val a = ArrayOf("x")
    val b = ArrayOf("y")
    Println(a != b)
}`,
			msg: []string{"!= cannot compare Array[string] values: Array[string] holds a Go slice", "compare with Equal: `!a.Equal(b)`"},
		},
		{
			name: "Array parameters",
			src: `func same(a Array[int], b Array[int]) bool = a == b

func main() { Println(same(ArrayOf(1), ArrayOf(1))) }`,
			msg: []string{"== cannot compare Array[int] values: Array[int] holds a Go slice", "compare with Equal: `a.Equal(b)`"},
		},
		{
			name: "Array parameters, not equal",
			src: `func differ(a Array[int], b Array[int]) bool = a != b

func main() { Println(differ(ArrayOf(1), ArrayOf(2))) }`,
			msg: []string{"!= cannot compare Array[int] values: Array[int] holds a Go slice", "compare with Equal: `!a.Equal(b)`"},
		},
		{
			name: "Option of Array parameters",
			src: `func differ(a Option[Array[int]], b Option[Array[int]]) bool = a != b

func main() { Println(differ(Some(ArrayOf(1)), None[Array[int]]())) }`,
			msg: []string{"!= cannot compare Option[Array[int]] values: field Value of case Some has type Array[int], which holds a Go slice",
				"compare with Equal: `!a.Equal(b)`"},
		},
		{
			name: "mutable Array by value",
			src: `import "martianoff/gala/collection_mutable"

func same(a collection_mutable.Array[int], b collection_mutable.Array[int]) bool = a == b

func main() {
    val a = collection_mutable.ArrayOf(1)
    Println(same(*a, *a))
}`,
			msg: []string{"== cannot compare collection_mutable.Array[int] values: collection_mutable.Array[int] holds a Go slice"},
		},
		{
			name: "List of Arrays",
			src: `func main() {
    val a = ListOf(ArrayOf(1))
    val b = ListOf(ArrayOf(1))
    Println(a == b)
}`,
			msg: []string{"== cannot compare List[Array[int]] values: List[Array[int]] holds a Go slice", "compare with Equal: `a.Equal(b)`"},
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
			name: "struct with a function field",
			src: `struct H(f func(int) int)

func inc(x int) int = x + 1

func main() { Println(H(inc) == H(inc)) }`,
			msg: []string{"== cannot compare H values: field f has type func(int) int, a function",
				"compare the other fields, or the values the function returns"},
		},
		{
			name: "Go map",
			src: `func main() {
    var m map[string]int
    var n map[string]int
    Println(m == n)
}`,
			msg: []string{"== cannot compare map[string]int values: map[string]int is a Go map"},
		},
		{
			name: "struct with a Go struct field that is not comparable",
			src: `import "bytes"

struct Buf(b bytes.Buffer)

func main() { Println(Buf(bytes.Buffer{}) == Buf(bytes.Buffer{})) }`,
			msg: []string{"field b has type bytes.Buffer, a Go type that is not comparable"},
		},
		{
			name: "stable identifier holding an Array",
			src: `val Answer = ArrayOf(42)

func describe(xs Array[int]) string = xs match {
    case Answer => "answer"
    case _      => "other"
}

func main() { Println(describe(ArrayOf(1))) }`,
			msg: []string{"case Answer compares the matched value with Answer by ==, which cannot compare Array[int] values",
				"`case v if v.Equal(Answer) =>`"},
		},
		// Accepted: Go compiles these comparisons.
		{name: "comparable struct", src: `struct Pt(x int, y string)

func main() { Println(Pt(1, "a") == Pt(1, "a")) }`},
		{name: "struct with a pointer field", src: `struct Node(v int, next *Node)

func main() { Println(Node(1, nil) == Node(1, nil)) }`},
		{name: "struct with a function behind a pointer", src: `struct H(f *func(int) int)

func main() { Println(H(nil) == H(nil)) }`},
		{name: "Option of int", src: `func main() { Println(Some(1) == Some(1)) }`},
		{name: "List of int, compared by identity", src: `func main() { Println(ListOf(1) == ListOf(1)) }`},
		{name: "List vals and parameters", src: `func differ(a List[int], b List[int]) bool = a != b

func main() {
    val a = ListOf(1)
    val b = ListOf(1)
    Println(a == b, differ(a, b))
}`},
		{name: "HashMap parameters, HashMap, HashSet and TreeSet vals", src: `func same(a HashMap[string, int], b HashMap[string, int]) bool = a == b

func main() {
    val m = HashMapOf(("a", 1))
    val s = HashSetOf(1)
    val t = TreeSetOf(1)
    Println(same(m, HashMapOf(("a", 1))), m == HashMapOf(("a", 1)), s != HashSetOf(1), t == TreeSetOf(1))
}`},
		{name: "mutable collections by pointer", src: `import "martianoff/gala/collection_mutable"

func main() {
    val a = collection_mutable.ArrayOf(1)
    val m = collection_mutable.EmptyHashMap[string, int]()
    Println(a == collection_mutable.ArrayOf(1), m != collection_mutable.EmptyHashMap[string, int]())
}`},
		{name: "interface values", src: `func main() {
    val a any = 1
    val b any = ArrayOf(1)
    Println(a == b)
}`},
		{name: "type parameter constrained by comparable", src: `func same[T comparable](a T, b T) bool = a == b

func main() { Println(same(1, 1)) }`},
		{name: "generic struct over a comparable type parameter", src: `struct Box[T comparable](v T)

func (b Box[T]) Holds(x T) bool = b.v == x

func main() { Println(Box(1).Holds(1)) }`},
		{name: "generic call results inside a function generic in T", src: `func f[T any](x T) bool = ListOf(1).Head() == 1 && Some(2).Get() == 2

func main() { Println(f("a")) }`},
		{name: "comparisons with nil", src: `func main() {
    var f func(int) int = nil
    var m map[string]int
    var s []int
    Println(f == nil, m != nil, nil == s)
}`},
		{name: "Go comparable struct", src: `import "time"

func main() { Println(time.Unix(0, 0) == time.Unix(0, 0)) }`},
		{name: "stable identifier holding an int", src: `val Answer = 42

func describe(n int) string = n match {
    case Answer => "answer"
    case _      => "other"
}

func main() { Println(describe(42)) }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := header + tc.src
			_, err := newDocGuardTranspiler().Transpile(src, "main.gala")
			if tc.msg == nil {
				require.NoError(t, err)
				return
			}
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
