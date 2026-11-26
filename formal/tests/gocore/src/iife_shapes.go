// The Go the transpiler emits for GALA's `if` used as a value (the functions of
// formal/Gala/M5/IifeInst.lean), its generated-code header and `//line` comments
// dropped: an IIFE as a `var`'s value, as an argument and with an arm of statements,
// and a function's tail `if`, which needs none.
package main

import "fmt"

func g(x int) int {
	return x * 2
}

func pick(b bool, x int) int {
	var y = func() int {
		if b {
			return x
		} else {
			return g(x)
		}
	}()
	return y + 1
}

func blk(x int) int {
	var z = func() int {
		if x > 0 {
			var t = x + 1
			return t * 2
		} else {
			return 0
		}
	}()
	return z
}

func tailIf(b bool, x int) int {
	{
		if b {
			return x + 1
		} else {
			return g(x)
		}
	}
}

func argIf(b bool, x int) int {
	return g(func() int {
		if b {
			return x
		} else {
			return 0
		}
	}()) + 1
}

func armNest(b bool, x int) string {
	var y = func() string {
		if b {
			var t = x + 1
			{
				if t > 2 {
					return "big"
				} else {
					return "small"
				}
			}
		} else {
			return "none"
		}
	}()
	return y
}

func main() {
	fmt.Println(pick(true, 3), blk(4), tailIf(false, 5), argIf(true, 2), armNest(true, 3))
	fmt.Println(pick(false, 3), blk(-1), tailIf(true, 5), argIf(false, 2), armNest(true, 0), armNest(false, 9))
}
