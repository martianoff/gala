// A struct that embeds a type with methods has them promoted to it, which
// GoCore's method sets leave out (Go would print "named 1" by the promoted
// String): the run stops at the struct type, after the output before it.
package main

import "fmt"

type named struct{ n int }

func (x named) String() string { return fmt.Sprint("named ", x.n) }

func main() {
	fmt.Println("before")
	v := struct{ named }{named{1}}
	fmt.Println(v)
}
