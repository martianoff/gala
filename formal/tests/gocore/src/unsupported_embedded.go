// A struct that embeds an interface (here fmt.Stringer) has the interface's
// methods promoted to it, which GoCore's method sets leave out: the run stops
// at the struct type, after the output before it.
package main

import "fmt"

type named struct{ n int }

func (x named) String() string { return fmt.Sprint("named ", x.n) }

func main() {
	fmt.Println("before")
	var v any = struct{ fmt.Stringer }{named{1}}
	_, ok := v.(fmt.Stringer)
	fmt.Println(v, ok)
}
