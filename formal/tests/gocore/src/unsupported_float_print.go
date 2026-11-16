// Printing a float inside a struct: GoCore reports it (unsupported), never a
// guess. go run prints "before", then "{p 0}".
package main

import "fmt"

type pair struct {
	name  string
	value float64
}

func main() {
	fmt.Println("before")
	fmt.Println(pair{name: "p"})
}
