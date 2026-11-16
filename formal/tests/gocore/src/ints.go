// Integer arithmetic: wrapping by width, truncated division, shifts and bitwise
// operators, and exact untyped constants.
package main

import "fmt"

const big = 1 << 70
const small = big >> 68

func main() {
	var max int = 9223372036854775807
	min := -max - 1
	fmt.Println(max+1 == min, min/-1, min%-1)
	fmt.Println(-7/2, -7%2, 7/-2, 7%-2)
	var u uint8 = 250
	u += 10
	var i8 int8 = 127
	i8++
	fmt.Println(u, i8, uint8(u+250), int8(u)*3)
	x := -16
	fmt.Println(x>>2, x<<3, 1<<62, uint64(1)<<63, x>>70, uint8(200)>>9)
	fmt.Println(12&-5, 12|-5, 12^-5, 12&^5, ^7, ^uint8(7))
	fmt.Println(small, 'a'+1, 0x1F_FF, 0b101, 0o17)
	n := 3
	fmt.Println(1<<n, -5>>n)
}
