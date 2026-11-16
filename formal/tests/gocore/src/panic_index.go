// An index out of range: Go's runtime panic, by its exact text.
package main

import "fmt"

func at(xs []int, i int) int { return xs[i] }

func main() {
	xs := []int{1, 2, 3}
	fmt.Println(at(xs, 2))
	fmt.Println(at(xs, 5))
}
