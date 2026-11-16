// Go infers E = int8 here through S's core type (constraint type inference),
// which GoCore does not model; it reports the call (unsupported), never
// defaulting E to int from the untyped constant. go run prints "true".
package main

import "fmt"

func contains[S ~[]E, E comparable](s S, v E) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func main() {
	fmt.Println(contains([]int8{1}, 1))
}
