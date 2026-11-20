// A key added to a map while it is ranged over may or may not be produced: the run
// stops, here after deleting the only key and adding it back.
package main

import "fmt"

func main() {
	m := map[int]int{1: 1}
	for k := range m {
		fmt.Println("k", k)
		delete(m, k)
		m[k] = 5
	}
	fmt.Println(m)
}
