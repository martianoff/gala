// A division that panics before an IIFE (a function literal called at once, the
// transpiler's shape of GALA's `if` used as a value), in one operand list: the call
// is an O-node like any other (cmd/compile/internal/walk/order.go), so gc runs it
// first and "iife" is printed before the division panics. GoCore's model of gc's
// order (Order.gc) rewrites this statement; M5's order predicate leaves it out.
package main

import "fmt"

func main() {
	x, z := 1, 0
	fmt.Println(x/z + func() int {
		fmt.Println("iife")
		return 1
	}())
}
