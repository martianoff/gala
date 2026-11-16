// The text of a panic is made when it ends the run, after the deferred calls:
// an error's Error runs then, and never for a recovered panic.
package main

import "fmt"

type failure struct{ msg string }

func (f *failure) Error() string {
	fmt.Println("Error called")
	return f.msg
}

func recovered() {
	defer func() { recover() }()
	panic(&failure{"recovered, never printed"})
}

func main() {
	recovered()
	fmt.Println("after the recovered panic")
	f := &failure{"first"}
	defer func() { f.msg = "changed by a deferred call" }()
	panic(f)
}
