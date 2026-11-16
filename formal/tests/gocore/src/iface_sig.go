// A type implements an interface by its methods' names and signatures, both
// (Go spec, "Implementing an interface").
package main

import "fmt"

type namer interface{ Name() string }

type equaler[T any] interface{ Equal(T) bool }

type a struct{}

func (a) Name() string { return "a" }

type b struct{}

func (b) Name() int { return 1 }

type node struct{ v int }

func (n node) Equal(o node) bool { return n.v == o.v }

func main() {
	for _, x := range []any{a{}, b{}, 3} {
		_, ok := x.(namer)
		fmt.Print(ok, " ")
	}
	fmt.Println()
	n := node{1}
	_, byValue := any(n).(equaler[node])
	// *node has Equal(node), not Equal(*node).
	_, byPointer := any(&n).(equaler[*node])
	_, pointerToValue := any(&n).(equaler[node])
	fmt.Println(byValue, byPointer, pointerToValue)
}
