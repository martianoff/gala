// A float operation stops GoCore (unsupported), never a guess. go run prints
// "before" and then "1".
package main

import "fmt"

func main() {
	var f float64
	fmt.Println("before")
	fmt.Println(f + 1)
}
