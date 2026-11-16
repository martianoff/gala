// %x of a Stringer hex-encodes its String text, which GoCore does not model: it
// reports it. go run prints "before", then "726564".
package main

import "fmt"

type color int

func (c color) String() string { return "red" }

func main() {
	fmt.Println("before")
	fmt.Printf("%x\n", color(1))
}
