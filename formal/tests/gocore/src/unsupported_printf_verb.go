// A verb that does not fit its operand: Go prints %!d(string=go), which GoCore
// does not imitate; it reports it. go run prints "before", then "%!d(string=go)".
package main

import "fmt"

func main() {
	fmt.Println("before")
	fmt.Printf("%d\n", "go")
}
