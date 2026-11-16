// A construct outside GoCore stops the run when it is reached, after the
// output before it.
package main

import "fmt"

func main() {
	fmt.Println("before")
	m := map[string]int{"a": 1}
	fmt.Println(m["a"])
}
