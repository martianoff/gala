// Promotion goes through an embedded type without methods of its own: outer
// embeds named, so struct{ outer } has named's String too (Go prints
// "named 1"). The run stops at outer's struct type, after the output before it.
package main

import "fmt"

type named struct{ n int }

func (x named) String() string { return fmt.Sprint("named ", x.n) }

type outer struct{ named }

func main() {
	fmt.Println("before")
	v := struct{ outer }{outer{named{1}}}
	fmt.Println(v)
}
