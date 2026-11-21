// %T of a generic type whose type argument is a local type: gc names the argument
// main.L·N, N a counter GoCore does not keep, so it stops. go run prints "before",
// then "main.Box[main.L·1]".
package main

import "fmt"

type Box[X any] struct{ V X }

func main() {
	fmt.Println("before")
	type L struct{}
	fmt.Printf("%T\n", Box[L]{})
}
