// Ranging over a map of two or more entries stops the run: Go does not specify the
// order, and gc varies it from run to run.
package main

import "fmt"

func main() {
	m := map[int]int{1: 1, 2: 2}
	fmt.Println("before")
	for k := range m {
		fmt.Println(k)
	}
}
