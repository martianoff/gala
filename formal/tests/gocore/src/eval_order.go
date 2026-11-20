// Go's order of evaluation: the spec orders the calls of an expression left to
// right but leaves a variable read unordered against them, and gc performs the
// calls first. Each line reads a variable that a call in the same statement changes.
package main

import "fmt"

var a = 1

func bump() int {
	a = 10
	return 100
}

func g(x, y int) int { return x + y }

type pair struct{ x, y int }

func ret() (int, int) {
	a = 1
	return a, bump()
}

func main() {
	fmt.Println(a + bump())
	a = 1
	fmt.Println(bump() + a)
	a = 1
	fmt.Println(a, bump())
	a = 1
	fmt.Println(a + g(a, bump()))
	a = 1
	fmt.Println(a > 5 && bump() > 0, a)
	a = 1
	fmt.Println(pair{a, bump()})
	a = 1
	x, y := a, bump()
	fmt.Println(x, y)
	fmt.Println(ret())
	a = 1
	b := a * 2
	fmt.Println(b + bump(), a)
}
