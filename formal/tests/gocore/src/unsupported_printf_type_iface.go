// %T of a type with an interface type with methods in it: Go prints []error here and
// []interface { Error() string } for the literal interface, one type to GoCore, so it
// stops. go run prints "before", then "[]error".
package main

import "fmt"

func main() {
	fmt.Println("before")
	fmt.Printf("%T\n", []error{})
}
