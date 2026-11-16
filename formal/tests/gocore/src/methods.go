// Method sets and fmt: a value-receiver String applies to T and *T, a
// pointer-receiver String only to *T; struct printing, an unexported field
// (no methods called through it), a pointer at the top level, nil, Print's
// spacing, Sprintf's verbs.
package main

import "fmt"

type V struct{ n int }

func (v V) String() string { return fmt.Sprintf("V<%d>", v.n) }

type P struct{ n int }

func (p *P) String() string { return fmt.Sprintf("P<%d>", p.n) }

func (p *P) Inc() { p.n++ }

type Plain struct {
	A int
	B string
	C bool
}

type Outer struct {
	In   Plain
	Val  V
	hide V
	Ptr  *Plain
	Any  any
}

func main() {
	v := V{1}
	p := P{2}
	fmt.Println(v, &v)
	fmt.Println(p, &p)
	p.Inc()
	(&p).Inc()
	fmt.Println(p.n)
	fmt.Println(Plain{1, "x", true}, &Plain{2, "y", false})
	var np *Plain
	fmt.Println(np, Outer{In: Plain{3, "z", true}, Val: V{4}, hide: V{5}})
	fmt.Print("a", 1, 2, "b", "c", 3, true, "\n")
	s := fmt.Sprintf("%d|%s|%v|%t|%%|%v", 42, "str", V{6}, false, Plain{7, "w", true})
	fmt.Println(s, len(s))
	var a any = V{8}
	if w, ok := a.(V); ok {
		fmt.Println("V", w.n)
	}
	_, ok := a.(*P)
	fmt.Println(ok, a == V{8}, a != V{9})
}
