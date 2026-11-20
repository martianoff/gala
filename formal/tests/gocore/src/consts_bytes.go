// Local constant declarations (iota, a spec repeating the previous one, typed
// and untyped), shifts of untyped constants in a constant declaration and in a
// conversion, and []byte(s): a new array of the string's bytes, of gc's
// capacity when it does not depend on escape analysis (Go spec, "Constant
// declarations", "Operators", "Conversions to and from a string type").
package main

import "fmt"

func main() {
	const a, b = iota, iota * 2
	const (
		x = 1 << iota
		y
		z
	)
	const name, typed = "go", int8(-3)
	const wide = 32 << (^uint(0) >> 63)
	var s uint = 3
	fmt.Println(a, b, x, y, z, name, typed, wide)
	fmt.Println(uint64(1<<s), int8(1<<s), uint16(1<<(s+10)), uint8(255>>s))
	bs := []byte("héllo")
	bs[0] = 'H'
	fmt.Println(len(bs), bs)
	long := []byte("a string of forty bytes, past the buffer")
	fmt.Println(len(long), cap(long))
	for _, c := range []byte("ab") {
		fmt.Print(c, " ")
	}
	fmt.Println()
}
