// `m[k] op= y` on a map entry: a nil map panics taking the slot, before the shift would
// (cmd/compile order.go OASOP).
package main

import "fmt"

func main() {
	s := -1
	zero := 0
	_, _ = s, zero
	var m map[string]int
	defer func() { fmt.Println(len(m), m) }()
	m["new"] <<= s
}
