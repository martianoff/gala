// An assignment's index is checked when the assignment is carried out, after the
// right side ran (Go spec, "Assignment statements").
package main

import "fmt"

func f() int {
	fmt.Println("right side")
	return 7
}

func main() {
	xs := []int{1, 2}
	xs[0], xs[1] = 10, 20
	fmt.Println(xs)
	xs[5] = f()
}
