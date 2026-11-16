// Deferred calls and recover (Go spec, "Defer statements", "Handling panics").
package main

import "fmt"

type counter struct{ n int }

func (c counter) show(tag string) { fmt.Println(tag, c.n) }

func (c *counter) inc() { c.n++ }

// Deferred calls run newest first, with the arguments they had at the defer.
func order() {
	for i := 0; i < 3; i++ {
		defer show(i)
	}
	fmt.Println("body done")
}

func show(i int) { fmt.Println("deferred", i) }

// A deferred closure sees the variables as they are when it runs, and can set a
// named result.
func named() (r int) {
	x := 1
	defer func() { r = r*10 + x }()
	x = 2
	return 3
}

// recover stops a panic; the function returns its results as they are.
func safeDiv(a, b int) (q int, err string) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Sprint("recovered: ", e)
		}
	}()
	if b == 0 {
		panic("division by zero")
	}
	return a / b, ""
}

// Without a panic, recover returns nil.
func calm() (s string) {
	defer func() { s = fmt.Sprint(recover() == nil) }()
	return "x"
}

// recover called by a function the deferred call calls does not stop the panic,
// and an unnamed result is zero after a recovered panic.
func helper() any { return recover() }

func indirect() (out int) {
	defer func() {
		if r := recover(); r != nil {
			out = 7
		}
	}()
	defer func() { fmt.Println("helper got", helper() == nil) }()
	panic("deep")
}

func unnamed() int {
	defer func() { recover() }()
	panic("gone")
}

// A method value: its receiver is evaluated at the defer.
func methods() {
	c := counter{1}
	defer c.show("value receiver saw")
	p := &counter{5}
	defer p.inc()
	defer func() { fmt.Println("pointer before inc", p.n) }()
	c.n = 9
}

// A deferred function's own deferred closure runs when that function returns: its
// recover is nil, and the outer panic goes on (to main's recover here).
func nestedDefer() (s string) {
	defer func() {
		defer func() {
			r := recover()
			fmt.Println("nested recover is nil:", r == nil)
			if r != nil {
				s = "recovered by nested"
			}
		}()
		fmt.Println("deferred body")
	}()
	panic("boom")
}

func catch() {
	defer func() { fmt.Println("caught:", recover()) }()
	fmt.Println(nestedDefer())
}

func main() {
	catch()
	order()
	fmt.Println(named())
	fmt.Println(safeDiv(7, 2))
	fmt.Println(safeDiv(1, 0))
	fmt.Println(calm())
	fmt.Println(indirect())
	fmt.Println(unnamed())
	methods()
}
