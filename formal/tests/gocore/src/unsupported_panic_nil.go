// panic(nil) panics with a *runtime.PanicNilError (Go 1.21), which recover
// returns: a runtime error GoCore does not model, so it reports it. go run
// prints "before", then "recovered: true".
package main

import "fmt"

func main() {
	defer func() { fmt.Println("recovered:", recover() != nil) }()
	fmt.Println("before")
	panic(nil)
}
