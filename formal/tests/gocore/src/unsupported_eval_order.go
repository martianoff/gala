// Where gc's order of evaluation depends on a static type GoCore does not have, the
// run stops. A struct value converted to an interface is copied at its place when gc
// cannot take its address (`pair{a, 0}` here, which go run prints as {1 0}), and read
// after the call otherwise (a package-level variable): GoCore, without types, does not
// guess which.
package main

import "fmt"

var a = 1

type pair struct{ x, y int }

func bump() int {
	a = 10
	return 100
}

func main() {
	fmt.Println("before")
	fmt.Println(pair{a, 0}, bump())
}
