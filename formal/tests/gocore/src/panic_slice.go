// A slice expression past the capacity: Go's runtime panic, by its exact text.
package main

import "fmt"

func window(xs []int, lo, hi, max int) []int { return xs[lo:hi:max] }

func main() {
	xs := make([]int, 2, 5)
	fmt.Println(window(xs, 0, 4, 5))
	fmt.Println(window(xs, 1, 3, 6))
}
