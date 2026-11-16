// An append that grows a slice from empty gets, from gc, a 32-byte stack buffer
// when escape analysis finds the result does not escape, and a heap block sized
// by growslice otherwise. GoCore does not model escape analysis, so the
// capacity is one of two, and observing it stops the run (unsupported). Here go
// run prints 4, the stack buffer's capacity for int.
package main

import "fmt"

func main() {
	var s []int
	s = append(s, 1)
	fmt.Println(len(s))
	fmt.Println(cap(s))
}
