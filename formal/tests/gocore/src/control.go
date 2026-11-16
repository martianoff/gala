// Control flow and scopes: loops with break and continue, per-iteration loop
// variables captured by closures, switches, type switches, parallel
// assignment, `:=` redeclaring, named results, shadowing.
package main

import "fmt"

type Shape interface{ Area() int }

type Sq struct{ s int }

func (q Sq) Area() int { return q.s * q.s }

type Rect struct{ w, h int }

func (r *Rect) Area() int { return r.w * r.h }

func divmod(a, b int) (q, r int) {
	q = a / b
	r = a % b
	return
}

func kind(x any) string {
	switch v := x.(type) {
	case nil:
		return "nil"
	case int, bool:
		return fmt.Sprintf("int or bool %v", v)
	case Shape:
		return fmt.Sprintf("shape %d", v.Area())
	default:
		return "other"
	}
}

func main() {
	sum := 0
	var last func() int
	for i := 0; i < 10; i++ {
		if i == 7 {
			break
		}
		if i%2 == 0 {
			continue
		}
		sum += i
		if i == 5 {
			last = func() int { return i }
		}
	}
	fmt.Println(sum, last())
	a, b := 1, 2
	a, b = b, a
	b, c := 5, 6
	fmt.Println(a, b, c)
	q, r := divmod(17, 5)
	fmt.Println(q, r)
	x := 1
	{
		x := 2
		x++
	}
	if x := 10; x > 5 {
		fmt.Println("inner", x)
	}
	fmt.Println("outer", x)
	switch {
	case x > 5:
		fmt.Println("big")
	case x == 1:
		fmt.Println("one")
	default:
		fmt.Println("other")
	}
	switch x + 1 {
	case 1, 2:
		fmt.Println("small")
	}
	fmt.Println(kind(nil), kind(3), kind(true), kind(Sq{3}), kind(&Rect{2, 5}), kind(Rect{1, 1}), kind("s"))
}
