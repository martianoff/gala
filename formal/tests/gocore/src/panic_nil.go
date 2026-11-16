// A nil pointer dereference panics with Go's runtime message, after the output
// before it.
package main

import "fmt"

type T struct{ n int }

func main() {
	var p *T
	fmt.Println("before")
	fmt.Println(p.n)
}
