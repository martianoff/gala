// gc reads these operands after the calls of their statement, whatever their type,
// where left to right reads them before: a field through a pointer parameter, a
// receiver dereferenced for a value-receiver method, a field through an assertion to a
// pointer type, and a receiver after a call of a local function value.
package main

import "fmt"

type pair struct{ x, y int }

type named struct{ n int }

func (x named) String() string { return fmt.Sprint("named ", x.n) }

type box struct {
	v int
	p pair
}

func (b box) get(n int) int { return b.v + n }

// Changes the box behind p: its field p, a struct (copied at its place if gc could not
// take its address), and v.
func set(p *box) int {
	p.p = pair{9, 9}
	p.v = 10
	return 100
}

var i any = &box{v: 1}

func reassign() int {
	i = &box{v: 2}
	return 100
}

// A field through a pointer parameter.
func viaParam(p *box) {
	fmt.Println(p.p, set(p))
}

// A value-receiver method of a pointer: the receiver is dereferenced at the call.
func viaReceiver(p *box) {
	fmt.Println(p.get(set(p)))
}

// A local function value of known arity as the only argument.
func viaFuncValue(p *box, g func() int) {
	fmt.Println(p.get(g()))
}

// A function literal of a package-level variable: its local `n`, assigned by a nested
// literal, is read after the call.
var h = func() {
	n := 1
	inc := func() int { n = 7; return 1 }
	fmt.Println(n + inc())
}

func main() {
	viaParam(&box{v: 1})
	viaReceiver(&box{v: 1})
	fmt.Println(i.(*box).v, reassign())
	b := &box{v: 1}
	viaFuncValue(b, func() int { b.v = 7; return 1 })
	h()
	// A local of interface type is passed to fmt as it is: read after the call.
	var st fmt.Stringer = named{1}
	swap := func() int { st = named{2}; return 0 }
	fmt.Println(st, swap())
}
