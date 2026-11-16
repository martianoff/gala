// Interface values: a nil pointer inside an interface is not a nil interface,
// comparison by dynamic type and value, an interface variable reassigned to
// another dynamic type, a method value saved through a pointer, `init`
// functions, and fmt printing `<nil>` for a nil receiver whose method panics.
package main

import "fmt"

type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

type T struct{ x int }

func (t T) Show() int { return t.x }

func (t *T) Name() string { return fmt.Sprintf("T%d", t.x) }

type V struct{ n int }

func (v V) String() string { return fmt.Sprintf("V%d", v.n) }

var order = "var"

func init() { order += " init1" }

func init() { order += " init2" }

func find(ok bool) error {
	var p *MyErr
	if ok {
		return nil
	}
	return p
}

func main() {
	fmt.Println(order)
	fmt.Println(find(true) == nil, find(false) == nil)
	var a, b any = int8(1), 1
	fmt.Println(a == b, a == any(int8(1)), b == 1)
	var x any = 1
	x = "s"
	fmt.Println(x)
	p := &T{1}
	f := p.Show
	p.x = 2
	fmt.Println(f(), p.Show())
	var np *T
	fmt.Println(np)
	var e error
	fmt.Println(e == nil)
	var nv *V
	fmt.Println(nv, V{3})
}
