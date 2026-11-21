// %T and %c: a type as reflect prints it (package names, type arguments joined by a
// comma alone, a variadic parameter, several results), the dynamic type of an
// interface and <nil> for none; a rune, U+FFFD for what is no rune, and padding.
package main

import "fmt"

type T int

func (T) String() string { return "a T" }

type S struct{ A int }

type Box[X any] struct{ V X }

type Pair[X, Y any] struct {
	A X
	B Y
}

func local() any {
	type L struct{}
	return L{}
}

func gen[X any]() any {
	type G struct{}
	return G{}
}

func main() {
	var a any
	var ip *int
	var st fmt.Stringer = T(1)
	fmt.Printf("%T|%T|%T|%T|%T\n", 1, "s", true, 1.5, 'x')
	fmt.Printf("%T|%T|%T|%T\n", T(1), S{}, &S{}, []int{})
	fmt.Printf("%T|%T|%T\n", [3]string{}, map[string]int{}, map[T][]*S{})
	fmt.Printf("%T|%T|%T\n", Box[int]{}, Box[T]{}, Box[Box[string]]{})
	fmt.Printf("%T|%T\n", Pair[int, string]{}, Pair[*T, []T]{})
	fmt.Printf("%T|%T|%T\n", func() {}, func(int, string) (bool, *S) { return false, nil }, func(...int) int { return 0 })
	fmt.Printf("%T|%T|%T|%T\n", &Box[int]{}, st, a, ip)
	fmt.Printf("%T|%T|%T\n", struct{}{}, []any{}, Box[struct{}]{})
	fmt.Printf("%T|%T|%T\n", int8(1), uint64(2), float32(1))
	fmt.Printf("%T|%T\n", local(), gen[int]())
	fmt.Printf("%5T|%-6T|%.3T|%v\n", 1, "s", T(2), 2)
	fmt.Printf("%c|%c|%c|%c|%c|%c\n", 'a', 65, 0x4e16, rune(-1), 0x110000, 0xD800)
	fmt.Printf("%c|%3c|%-3c|%c|\n", byte('z'), 'b', 'c', T(66))
}
