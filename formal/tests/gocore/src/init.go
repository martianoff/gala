// Package initialization: variables in dependency order, a local that shadows
// a package variable not counting as a reference, constants with iota.
package main

import "fmt"

var a = first()
var b = second()
var c = d + 1
var d = third()

const (
	X = iota * 10
	Y
	Z
	_
	W
)

func first() int {
	b := 1
	fmt.Println("first")
	return b
}

func second() int {
	fmt.Println("second")
	return 2
}

func third() int {
	fmt.Println("third")
	return a + 10
}

func main() {
	fmt.Println(a, b, c, d, X, Y, Z, W)
}
