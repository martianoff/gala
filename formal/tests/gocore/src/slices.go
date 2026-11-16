// Slices, arrays and variadic calls: GoCore's model of them, and the gc
// runtime's append growth (formal/Gala/GoCore/GcRuntime.lean), against go run.
package main

import "fmt"

type triple struct{ a, b, c int }

// big holds a pointer and is over 512 bytes: its blocks carry a malloc header.
type big struct {
	p   *int
	pad [70]int
}

// growth prints each capacity a slice of n appends passes through. It starts
// with capacity 1, so no append grows it from empty, where gc may use a stack
// buffer depending on escape analysis (see unsupported_escape.go).
func growthInts(n int) []int {
	s := make([]int, 0, 1)
	caps := make([]int, 0, 1)
	last := -1
	for i := 0; i < n; i++ {
		s = append(s, i)
		if cap(s) != last {
			last = cap(s)
			caps = append(caps, last)
		}
	}
	return caps
}

func growthBytes(n int) []int {
	s := make([]byte, 0, 1)
	caps := make([]int, 0, 1)
	last := -1
	for i := 0; i < n; i++ {
		s = append(s, byte(i))
		if cap(s) != last {
			last = cap(s)
			caps = append(caps, last)
		}
	}
	return caps
}

func growthStrings(n int) []int {
	s := make([]string, 0, 1)
	caps := make([]int, 0, 1)
	last := -1
	for i := 0; i < n; i++ {
		s = append(s, "x")
		if cap(s) != last {
			last = cap(s)
			caps = append(caps, last)
		}
	}
	return caps
}

func growthTriples(n int) []int {
	s := make([]triple, 0, 1)
	caps := make([]int, 0, 1)
	last := -1
	for i := 0; i < n; i++ {
		s = append(s, triple{i, i, i})
		if cap(s) != last {
			last = cap(s)
			caps = append(caps, last)
		}
	}
	return caps
}

func growthBig(n int) []int {
	s := make([]big, 0, 1)
	caps := make([]int, 0, 1)
	last := -1
	for i := 0; i < n; i++ {
		s = append(s, big{})
		if cap(s) != last {
			last = cap(s)
			caps = append(caps, last)
		}
	}
	return caps
}

func growthEmpty(n int) []int {
	s := make([]struct{}, 0, 1)
	caps := make([]int, 0, 1)
	last := -1
	for i := 0; i < n; i++ {
		s = append(s, struct{}{})
		if cap(s) != last {
			last = cap(s)
			caps = append(caps, last)
		}
	}
	return caps
}

func sum(label string, xs ...int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	fmt.Println(label, len(xs), cap(xs), xs == nil, total)
	return total
}

func bump(xs ...int) {
	for i := range xs {
		xs[i]++
	}
}

func main() {
	fmt.Println(growthInts(2000))
	fmt.Println(growthBytes(3000))
	fmt.Println(growthStrings(1000))
	fmt.Println(growthTriples(700))
	fmt.Println(growthBig(40))
	fmt.Println(growthEmpty(10))
	// Several values at once grow past double.
	fmt.Println(cap(append([]int{1}, 2, 3, 4, 5)))

	// Grown from empty, a slice may have a stack buffer's capacity or a heap
	// block's: GoCore runs it while nothing tells them apart.
	var acc []int
	for i := 0; i < 10; i++ {
		acc = append(acc, i*i)
	}
	fmt.Println(acc, len(acc))

	// Aliasing: an append within capacity writes into the shared array; one past
	// it does not.
	s := make([]int, 3, 4)
	t := append(s[:2], 9)
	fmt.Println(s, t, len(t), cap(t))
	u := append(s, 1, 2)
	u[0] = 100
	fmt.Println(s, u, len(u), cap(u))
	v := append(s[:3], 7)
	v[0] = 42
	fmt.Println(s, v)

	// Full slice expressions, arrays and copy.
	a := [5]int{1, 2, 3, 4, 5}
	w := a[1:3:4]
	fmt.Println(w, len(w), cap(w))
	w = append(w, 30)
	fmt.Println(a, w)
	w = append(w, 40)
	w[0] = -1
	fmt.Println(a, w)
	b := a
	b[0] = 99
	fmt.Println(a == b, a[0], b[0], a == [5]int{1, 2, 3, 30, 5})
	n := copy(a[:], []int{7, 8})
	fmt.Println(n, a)
	n = copy(a[1:], a[:])
	fmt.Println(n, a)
	bs := make([]byte, 3)
	fmt.Println(copy(bs, "héllo"), bs)

	// Literals: keyed, [...] and elided element types.
	keyed := []string{3: "d", 1: "b", "c"}
	fmt.Println(len(keyed), keyed)
	arr := [...]int{2: 5, 7}
	fmt.Println(len(arr), arr)
	ps := []*triple{{1, 2, 3}, {a: 4}}
	fmt.Println(*ps[0], ps[1].a)
	grid := [][]int{{1, 2}, {3}}
	fmt.Println(grid, len(grid[1]))

	// Variadic calls: none, several, and a slice passed with ... itself.
	sum("none")
	sum("three", 1, 2, 3)
	xs := []int{1, 2, 3}
	sum("spread", xs...)
	bump(xs...)
	fmt.Println(xs)
	bump(xs[1:]...)
	fmt.Println(xs)
	fmt.Println(append(xs, xs...))

	// Range over a slice reads the elements as it reaches them; over an array, its
	// copy; over a string, its runes.
	ys := []int{1, 2, 3}
	for i, y := range ys {
		if i == 0 {
			ys[2] = 30
		}
		fmt.Print(y, " ")
	}
	fmt.Println()
	arr2 := [3]int{1, 2, 3}
	for i, y := range arr2 {
		if i == 0 {
			arr2[2] = 30
		}
		fmt.Print(y, " ")
	}
	fmt.Println(arr2)
	for i, r := range "aé!" {
		fmt.Print(i, ":", r, " ")
	}
	fmt.Println()
	for i := range 3 {
		fmt.Print(i)
	}
	fmt.Println()

	// nil slices.
	var nilS []int
	fmt.Println(nilS == nil, len(nilS), cap(nilS), nilS[:0] == nil, nilS)
	fmt.Println(len("héllo"[1:3]), "hello"[1:3], "hello"[4])
	p := &arr2
	fmt.Println(p[1], len(p), p[:2])
	q := &[...]int{4, 5}
	fmt.Println(len(q), cap(q), q[1])
	var np *[3]int
	fmt.Println(len(np), cap(np))
	var last int32
	for i := range 'z' {
		last = i * 100000000
	}
	fmt.Println(last)
}
