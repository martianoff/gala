// Comparing interface values that hold floats compares the floats: GoCore
// reports it (unsupported), never a guess. go run prints "before", then "true".
package main

import "fmt"

func main() {
	var z float64
	var a, b any = z, z
	fmt.Println("before")
	fmt.Println(a == b)
}
