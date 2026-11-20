// GoCore runs maps of integer, string and boolean keys only: not a float key, whose
// NaN and signed zeros Go's maps treat specially.
package main

import "fmt"

func main() {
	fmt.Println("before")
	m := map[float64]int{1.5: 1}
	fmt.Println(m)
}
