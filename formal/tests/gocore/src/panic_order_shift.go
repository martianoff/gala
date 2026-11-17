// Two failing operands, neither a call: the Go spec leaves their order open, and
// gc evaluates them left to right, so the shift panics first.
package main

import "fmt"

func main() {
	x, y, z := 1, -1, 0
	fmt.Println((x << y) + x/z)
}
