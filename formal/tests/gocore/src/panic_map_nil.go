// A store into a nil map panics with the runtime error gc raises.
package main

import "fmt"

func main() {
	var m map[string]int
	fmt.Println("before", m["k"])
	m["k"]++
	fmt.Println("never")
}
