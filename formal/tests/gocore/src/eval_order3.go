// gc's order pass (cmd/compile/internal/walk/order.go) evaluates more than calls at
// their place: builtins, slice expressions and && and || too; plain reads come after.
// Each line has an operand that a call in the same statement changes.
package main

import "fmt"

var a = 1
var s = []int{1, 2, 3}

type counter struct{ n int }

func (c *counter) inc() int {
	c.n++
	return 100
}

func (c counter) get() int { return c.n }

var p = &counter{}

func bump() int {
	a = 10
	s = append(s, 4)
	return 100
}

func reset() {
	a = 1
	s = []int{1, 2, 3}
	p = &counter{}
}

func show(x, y int) { fmt.Println("deferred", x, y) }

type pt struct{ x, y int }

// A struct passed to a parameter of struct type is not converted to an interface, so gc
// reads it after the calls of the statement.
func showPt(q pt, n int) { fmt.Println("pt", q, n) }

func pairOf() (int, int) {
	return a, bump()
}

func main() {
	fmt.Println(len(s) + bump())
	reset()
	fmt.Println(bump() + len(s))
	reset()
	fmt.Println(s[1:], bump())
	reset()
	fmt.Println(a < 5 && a > 0, bump())
	reset()
	fmt.Println(a > 5 || a < 5, bump(), a)
	reset()
	x := a + bump()
	fmt.Println(x)
	reset()
	var y = a + bump()
	fmt.Println(y)
	reset()
	if a+bump() > 105 {
		fmt.Println("if saw 110")
	}
	reset()
	z := a
	z += bump()
	fmt.Println(z)
	reset()
	fmt.Println(fmt.Sprint(a) + fmt.Sprint(bump()))
	reset()
	fmt.Println(a + func() int { a = 5; return 1 }())
	reset()
	n := 1
	inc := func() int { n = 7; return 1 }
	fmt.Println(n + inc())
	fmt.Println(p.n + p.inc())
	reset()
	fmt.Println(p.get() + p.inc())
	reset()
	fmt.Println(pairOf())
	reset()
	showPt(pt{a, 0}, bump())
	reset()
	t := make([]int, 11)
	t[a] = bump()
	fmt.Println(t[1], t[10])
	reset()
	for i := 0; i < 2; i++ {
		fmt.Println(i, a+bump())
		a = 1
	}
	reset()
	defer show(a, bump())
	a = 2
}
