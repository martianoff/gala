// float64 and float32: exact untyped constants, IEEE 754 arithmetic rounded to
// the type's width, conversions, and fmt's %v (strconv's shortest 'g').
package main

import "fmt"

func pick[T any](a, b T) T { return b }

type celsius float64

const third = 1.0 / 3

func main() {
	// %v: the shortest digits, %e form below 1e-4 or from 1e+06 on.
	for _, x := range []float64{0, 1, 0.1, 2.5, 100000, 1e6, 123456789, 1e20, 1e21, 1e-4, 1e-5,
		1.0 / 3, 5e-324, 1.7976931348623157e308, -0.5, 3.14159, 1e100} {
		fmt.Print(x, " ")
	}
	fmt.Println()

	// Untyped constants are exact: 0.1 + 0.2 is 0.3 before it becomes a float64;
	// at run time it is not.
	a, b := 0.1, 0.2
	fmt.Println(0.1+0.2, a+b, a+b == 0.3, third*3 == 1)
	// The kind order: an untyped float constant makes the operation float.
	fmt.Println(1/2, 1/2.0, 'a'*0.5, 7/2*2.0)
	// Type inference takes the float constant's kind: T is float64.
	fmt.Println(pick(1, 2.5))

	// Division by zero of floats is an infinity or NaN, not a panic.
	zero := 0.0
	inf, nan := 1/zero, zero/zero
	fmt.Println(inf, -inf, nan, nan == nan, nan < 1, inf > 1e308, -zero, 1/(-zero))

	// No fused multiply-add: x*y+z rounds the product first (fused, this is 5.551115123125783e-17).
	x, y, z := 0.1, 10.0, -1.0
	fmt.Println(x*y + z)

	// float32: each operation rounds to binary32.
	var f32 float32 = 16777216
	fmt.Println(f32+1, float32(16777217), float32(0.1), float64(float32(0.1)))
	var s32 float32
	var s64 float64
	for i := 0; i < 10; i++ {
		s32 += 0.1
		s64 += 0.1
	}
	fmt.Println(s32, s64)

	// Conversions: to integers by truncation; from large integers, rounded.
	p, q := 2.9, -2.9
	fmt.Println(int(p), int(q), int64(1e18), float64(9007199254740993), uint8(200.0))
	n := 9007199254740993
	fmt.Println(float64(n), int(float64(n)))
	var c celsius = 36.6
	fmt.Println(c, float64(c)*2, c > 30)

	// Constants have no negative zero: -z and z * -1 are folded exactly; a negative
	// constant too small for the type rounds to +0.
	w, v := -z0, z0*-1
	var tiny float64 = -1e-400
	fmt.Println(w, 1/w, v, 1/v, tiny, 1/tiny, float32(-1e-50))
	// A typed constant is its type's value, and typed constant arithmetic rounds to
	// the type.
	fmt.Println(third32, third32*3, float64(third32), big32/4*2)
	// An integral float constant where Go needs an integer.
	var arr [2.0]int
	s := []int{1, 2}
	fmt.Println(len(arr), s[1.0], 1<<2.0, len(make([]int, 2.0)))
	// A float32 constant rounds once, from the exact value: 1 + 2^-24 + 2^-60 is
	// above the float32 tie, so it rounds up; through float64 it would land on the
	// tie 1 + 2^-24 and round to even, to 1.
	fmt.Println(once32, float64(once32) > 1)
}

const once32 float32 = 1 + 1.0/(1<<24) + 1.0/(1<<60)

const z0 float64 = 0

const third32 float32 = 1.0 / 3

const big32 float32 = 3e38
