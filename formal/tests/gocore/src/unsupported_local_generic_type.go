// A generic type declared in a function body, which gc 1.25.5 accepts: GoCore
// does not model it, so it reports it. go run prints "before", then "{1} {a}".
package main

import "fmt"

func main() {
	fmt.Println("before")
	type Box[T any] struct{ V T }
	fmt.Println(Box[int]{1}, Box[string]{"a"})
}
