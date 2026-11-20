// Maps (Go spec, "Map types", "Index expressions", "Deletion of map elements",
// "Length and capacity"): nil maps, literals, comma-ok, op= and ++ on entries,
// sharing, a defined map type, a single-entry range, and fmt's sorted printing
// (fmt/print.go printValue, internal/fmtsort).
package main

import "fmt"

type S struct {
	A int
	B string
}
type T int

func (t T) String() string { return fmt.Sprintf("T%d", int(t)) }

type Counts map[string]int

func get(m map[string]int, k string) int { return m[k] }

func main() {
	var nm map[string]int
	fmt.Println(nm, len(nm), nm == nil, nm["x"])
	v, ok := nm["x"]
	fmt.Println(v, ok)
	delete(nm, "x")
	m := map[string]int{"b": 2, "a": 1, "c": 3}
	fmt.Println(m)
	fmt.Printf("%v|%d\n", m, len(m))
	m["a"] += 10
	m["z"]++
	delete(m, "b")
	delete(m, "nope")
	fmt.Println(m, len(m), get(m, "a"), get(m, "q"))
	var w, found = m["z"]
	fmt.Println(w, found)
	im := map[int]string{-3: "x", 10: "y", 2: "z"}
	fmt.Println(im, im[2], im[7] == "")
	bm := map[bool]int{true: 1, false: 0}
	fmt.Println(bm)
	sm := map[string]S{"k": {1, "x"}}
	fmt.Println(sm)
	tm := map[T]T{3: 4, 1: 2}
	fmt.Println(tm)
	mm := map[string]map[string]int{"o": {"y": 1, "x": 2}, "a": nil}
	fmt.Println(mm, mm["a"] == nil, len(mm["o"]))
	u8 := map[uint8]int{200: 1, 3: 2}
	fmt.Println(u8)
	alias := m
	alias["shared"] = 7
	fmt.Println(m["shared"], len(m))
	c := Counts{"q": 1}
	c["r"] = 2
	fmt.Println(c, len(c))
	mk := make(map[int]int, 10)
	mk[1] = 1
	for k, v := range mk {
		fmt.Println("range", k, v)
		delete(mk, k)
	}
	fmt.Println(mk)
	for range nm {
		fmt.Println("never")
	}
	var e map[string]int = map[string]int{}
	fmt.Println(e, e == nil)
	var anyv interface{} = m
	fmt.Println(anyv)
}
