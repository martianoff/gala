// Type inference GoCore runs: an untyped constant's default type for a parameter
// whose constraint is an interface of methods only (no core type); a generic defined
// function type against a function literal (inexact unification); and a generic
// function passed by name, inferred with the call.
package main

import "fmt"

type Shower interface{ Show() string }

type N int

func (n N) Show() string { return fmt.Sprint("N", int(n)) }

func tag[U any, C Shower](c C, u U) string { return fmt.Sprintf("%s %T %v", c.Show(), u, u) }

type Conv[A, B any] func(A) B

func apply[A, B any](a A, f Conv[A, B]) B { return f(a) }

func id[T any](t T) T { return t }

func pairOf[T any](t T) [2]T { return [2]T{t, t} }

func mapAll[T, U any](xs []T, f func(T) U) []U {
	var out []U
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

func both[T, U, V any](x T, f func(T) U, g func(U) V) V { return g(f(x)) }

func main() {
	fmt.Println(tag(N(1), 7), tag(N(2), 'x'), tag(N(3), 1.5), tag(N(4), "s"))
	r := apply("gala", func(s string) int { return len(s) })
	fmt.Printf("%T %v\n", r, r)
	fmt.Printf("%T %v\n", apply(3, func(n int) []int { return []int{n, n} }), apply(2, func(n int) bool { return n > 1 }))
	a := mapAll([]int{1, 2}, id)
	b := mapAll([]string{"a", "b"}, pairOf)
	fmt.Printf("%T %v %T %v\n", a, a, b, b)
	c := both(N(5), id, pairOf)
	fmt.Printf("%T %v\n", c, c)
}
