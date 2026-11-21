// A division that panics before a call, in one operand list: the Go spec leaves
// their order open, and gc calls f first (cmd/compile/internal/walk/order.go), so
// "f ran" is printed before the division panics. Left to right, the order GALA
// and galaspec use, it would panic without printing. GoCore's model of gc's order
// (Order.gc) rewrites this statement; M5's order predicate leaves it out.
package main

import "fmt"

func f(x int) int {
	fmt.Println("f ran")
	return x
}

func main() {
	x, z := 1, 0
	fmt.Println(x/z + f(x))
}
