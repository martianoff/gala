// The transpiler's self-tail-call loop (M5 step 3c), as `gala transpile` emits it for
//
//	func sum(n int, acc int) int = if (n == 0) acc else sum(n - 1, acc + n)
//	func gcd(a int, b int) int = if (b == 0) a else gcd(b, a % b)
//	func sw(a int, b int, n int) int = if (n == 0) a - b else sw(b, a, n - 1)
//	func down(x int, n int) int = if (n < 0) x else down(show(x) + 10 / n, n - 1)
//
// (temporaries numbered across the program, `//line` comments dropped). `sw` swaps its
// parameters, so assigning them in place without the temporaries would differ
// (`sw(1, 2, 2)` would give 0, not -1). `down`'s argument divides by a parameter that
// reaches 0: each iteration prints first, then the last one's division panics, so the
// output before the panic and where it happens are pinned.
package main

import "fmt"

func sum(n int, acc int) int {
	for {
		if n == 0 {
			return acc
		} else {
			_tmp_1 := n - 1
			_tmp_2 := acc + n
			n, acc = _tmp_1, _tmp_2
			continue
		}
	}
}

func gcd(a int, b int) int {
	for {
		if b == 0 {
			return a
		} else {
			_tmp_3 := b
			_tmp_4 := a % b
			a, b = _tmp_3, _tmp_4
			continue
		}
	}
}

func sw(a int, b int, n int) int {
	for {
		if n == 0 {
			return a - b
		} else {
			_tmp_5 := b
			_tmp_6 := a
			_tmp_7 := n - 1
			a, b, n = _tmp_5, _tmp_6, _tmp_7
			continue
		}
	}
}

func show(n int) int {
	fmt.Println(fmt.Sprintf("step %d", n))
	return n
}

func down(x int, n int) int {
	for {
		if n < 0 {
			return x
		} else {
			_tmp_8 := show(x) + 10/n
			_tmp_9 := n - 1
			x, n = _tmp_8, _tmp_9
			continue
		}
	}
}

func main() {
	fmt.Println(sum(10, 0))
	fmt.Println(gcd(84, 36))
	fmt.Println(sw(1, 2, 2))
	fmt.Println(sw(1, 2, 3))
	fmt.Println(down(0, 3))
}
