// A deferred call that panics while its function panics: the new panic goes on,
// and Go prints it after the first. Deferred calls still all run.
package main

import "fmt"

func main() {
	defer note("main's deferred call ran")
	work()
}

func note(s string) { fmt.Println(s) }

func work() {
	defer func() { panic("second") }()
	defer func() { fmt.Println("cleanup ran") }()
	panic("first")
}
