package main

import "fmt"

// Package-level types a local type shadows: one with a String method, one a string.
type T int

func (T) String() string { return "package T" }

type B string

type square struct{ side int }

func (s square) Area() int { return s.side * s.side }

var x = 1

// A package-level constant a local one shadows.
const n = 2

// A local type's definition sees the local constants where it is written.
func lengths() {
	const n = 3
	const m = 4
	type A [n]int
	type C [m]string
	fmt.Println(len(A{}), len(C{}), A{})
}

func bump() int {
	x = 10
	return 5
}

// One type per declaration: every call gives the same type, another
// declaration of the same name another type.
func f() any {
	type T struct{ N int }
	return T{1}
}

func g() any {
	type T struct{ N int }
	return T{1}
}

// In a generic function, one type per instantiation, mentioned or not.
func gen[X any](x X) any {
	type Box struct{ V int }
	return Box{1}
}

func pair[X, Y any](x X, y Y) any {
	// Equal values: only the type arguments tell them apart.
	type Pair struct{}
	return Pair{}
}

// A function literal in a generic function: the function's instantiation.
func lit[X any](x X) any {
	h := func() any {
		type In struct{}
		return In{}
	}
	return h()
}

type G[X any] struct{ v X }

// A method of a generic type: the receiver's instantiation.
func (g G[X]) m() any {
	type R struct{}
	return R{}
}

func kind(v any) string {
	type T struct{ N int }
	if v == nil {
		v = T{7}
	}
	switch v.(type) {
	case T:
		return "local T"
	case int:
		return "int"
	}
	return "other"
}

// A closure that outlives the function declaring its type.
func maker() func(int) any {
	type C int
	return func(n int) any { return C(n) }
}

func main() {
	fmt.Println(f() == f(), f() == g(), f())
	fmt.Println(gen(1) == gen(1), gen(1) == gen("s"), gen(true))
	fmt.Println(pair(1, "a") == pair(1, "a"), pair(1, "a") == pair("a", 1), pair(1, "a") == pair(1, 2))
	fmt.Println(lit(1) == lit(1), lit(1) == lit("a"))
	fmt.Println(G[int]{3}.m() == G[int]{4}.m(), G[int]{3}.m() == G[string]{"3"}.m())
	fmt.Println(kind(f()), kind(nil), kind(1), kind("s"))
	mk := maker()
	fmt.Println(mk(1) == mk(1), mk(1) == mk(2), mk(4))

	// A recursive local type.
	type Node struct {
		V    int
		Next *Node
	}
	n := &Node{1, &Node{2, nil}}
	fmt.Println(n.V, n.Next.V, n.Next.Next == nil)

	// A declaration in a loop: one type, whatever the iteration.
	var ts []any
	for i := 0; i < 3; i++ {
		type L int
		ts = append(ts, L(0))
	}
	fmt.Println(ts[0] == ts[1], ts[1] == ts[2], ts)

	// Shadowing a package-level type: the local T has no String method.
	{
		type T int
		fmt.Println(T(4))
		fmt.Printf("%v %d\n", T(4), T(4))
	}
	fmt.Println(T(4))

	// Specs bind one by one: A's B is the package's B, a string.
	type (
		A struct{ b B }
		B struct{ Q int }
	)
	fmt.Println(A{}, B{}, A{} == A{b: ""})

	// Shadowing a variable.
	y := 2
	{
		type y int
		fmt.Println(y(7) + 1)
	}
	fmt.Println(y)

	// A variable shadowing a local type, and the type again after its block.
	type z int
	{
		z := func(v int) int { return v * 10 }
		fmt.Println(z(7))
	}
	fmt.Println(z(7) + 1)
	lengths()

	// Aliases.
	type Al = int
	type AN = Node
	var a Al = 3
	fmt.Println(a+1, AN{V: 9}.V)

	// A local interface type.
	type Shape interface{ Area() int }
	var s Shape = square{3}
	fmt.Println(s.Area())

	// A conversion to a local type is not a call: gc reads x after bump(), 10 + 5.
	type I int
	sum := I(x) + I(bump())
	fmt.Println(sum)
}
