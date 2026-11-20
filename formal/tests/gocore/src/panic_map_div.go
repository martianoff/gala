// `m[k] op= y` on a map entry: gc rewrites /= on a map entry to m[k] = m[k] / y: the division panics before any store
// (cmd/compile order.go OASOP).
package main

import "fmt"

func main() {
	s := -1
	zero := 0
	_, _ = s, zero
	m := map[string]int{"k": 1}
	defer func() { fmt.Println(len(m), m) }()
	m["d"] /= zero
}
