// A generic function passed by name to a call whose other argument is an untyped
// constant: Go unifies the two functions' type parameters, then gives the constant
// its default type; GoCore infers in one direction only and stops. go run prints
// "before", then "[1]".
package main

import "fmt"

func id[T any](t T) T { return t }

func apply[T, U any](x T, f func(T) U) []U { return []U{f(x)} }

func main() {
	fmt.Println("before")
	fmt.Println(apply(1, id))
}
