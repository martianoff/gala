// fmt.Stringer, the defined interface type `interface { String() string }`
// (fmt/print.go): in a type switch, a type assertion, a variable, and as a type
// distinct from an identical literal interface (Go spec, "Type identity").
package main

import (
	"fmt"
	fm "fmt"
)

type named struct{ n int }

func (x named) String() string { return fmt.Sprint("named ", x.n) }

type plain int

type ptrOnly struct{}

func (p *ptrOnly) String() string { return "ptr" }

func describe(v any) string {
	switch s := v.(type) {
	case nil, string, int:
		return "basic"
	case fmt.Stringer:
		return "stringer " + s.String()
	case error:
		return "error"
	}
	return "other"
}

func main() {
	fmt.Println(describe(named{1}), describe(plain(2)), describe(3), describe(nil))
	fmt.Println(describe(ptrOnly{}), describe(&ptrOnly{}))
	var s fmt.Stringer
	fmt.Println(s == nil, s)
	s = named{2}
	fmt.Println(s, s.String())
	_, ok := any(plain(1)).(fmt.Stringer)
	_, ok2 := any(&ptrOnly{}).(fmt.Stringer)
	fmt.Println(ok, ok2)
	var lit interface{ String() string } = s
	s = lit
	fmt.Println(lit, s)
	// A func type with a fmt.Stringer parameter is not the one with the literal
	// interface: the two types are different.
	var f any = func(fmt.Stringer) {}
	_, same := f.(func(interface{ String() string }))
	_, self := f.(func(fmt.Stringer))
	fmt.Println(same, self)
	// As a conversion, a pointer's element, a slice's element, in parentheses, and
	// through another import name.
	conv := fmt.Stringer(named{3})
	var p *fmt.Stringer = &conv
	xs := []fmt.Stringer{named{4}, &ptrOnly{}}
	var q fm.Stringer = *p
	ys := [](fmt.Stringer){q}
	fmt.Println(conv, *p, xs[0], xs[1], len(xs), q, ys[0])
}
