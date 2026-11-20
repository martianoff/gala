// `m[k] op= y` on a map entry: gc takes the new entry's slot (adding it with 0) before the shift panics
// (cmd/compile order.go OASOP).
package main

import "fmt"

func main() {
	s := -1
	zero := 0
	_, _ = s, zero
	m := map[string]int{"k": 1}
	defer func() { fmt.Println(len(m), m) }()
	m["new"] <<= s
}
