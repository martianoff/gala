// fmt.Printf and Sprintf with flags, width and precision on integers, booleans
// and strings (fmt package documentation, "Printing").
package main

import "fmt"

type color int

func (c color) String() string { return [...]string{"red", "green"}[c] }

type odd int

func (o odd) String(prefix string) string { return prefix }

type wrapped struct{ v int }

func (w wrapped) Error() string { return fmt.Sprintf("err %d", w.v) }

func main() {
	n, err := fmt.Printf("%d %s %v %t\n", 42, "go", -7, true)
	fmt.Println(n, err == nil)
	fmt.Printf("[%5d] [%-5d] [%05d] [%+d] [% d] [%+05d] [%-05d]\n", 42, 42, 42, 42, 42, -42, 7)
	fmt.Printf("[%+ d] [%+ d] [% -5d] [%-+05d]\n", -5, 5, -3, 3)
	fmt.Printf("[%.3d] [%8.3d] [%08.3d] [%.0d] [%5.0d]\n", 7, 7, 7, 0, 0)
	fmt.Printf("[%x] [%X] [%#x] [%#X] [%08x] [%#08x] [%x] [%5x]\n", 255, 255, 255, 255, 255, 255, -255, 10)
	fmt.Printf("[%5s] [%-5s] [%.2s] [%5.1s] [%05s] [%s]\n", "go", "go", "héllo", "héllo", "ab", "")
	fmt.Printf("[%6t] [%-6t] [%v]\n", true, false, nil)
	fmt.Printf("[%6v] [%-6v] [%d] [%s] [%8v]\n", color(1), color(0), color(1), color(0), wrapped{3})
	fmt.Printf("100%%\n")
	s := fmt.Sprintf("%03d|%3d|%-3d|", 5, 5, 5)
	fmt.Println(s, len(s))
	// nil is padded, never truncated; a String method of another signature is no
	// Stringer.
	fmt.Printf("[%.2v][%8v][%.3v][% v]\n", nil, nil, odd(42), odd(42))
	k, _ := fmt.Println("hi")
	fmt.Println(k)
	var u8 uint8 = 200
	var i64 int64 = -9223372036854775808
	fmt.Printf("%d %x %d %X\n", u8, u8, i64, i64)
}
