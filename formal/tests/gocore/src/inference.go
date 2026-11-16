// Type arguments inferred through function-typed parameters, and given in part
// (Go spec, "Type inference", "Instantiations").
package main

import "fmt"

type box[T any] struct{ v T }

func mapBox[T, U any](b box[T], f func(T) U) box[U] { return box[U]{f(b.v)} }

func compose[A, B, C any](f func(A) B, g func(B) C) func(A) C {
	return func(a A) C { return g(f(a)) }
}

func fold[T, R any](xs []T, z R, f func(R, T) R) R {
	for _, x := range xs {
		z = f(z, x)
	}
	return z
}

func convert[To, From any](x From, f func(From) To) To { return f(x) }

func pick[T any](a, b T) T { return a }

func main() {
	// A typed argument decides T over an untyped constant before it: int8 wraps.
	fmt.Println(pick(1, int8(2)) + 127)
	// Untyped constants alone: a rune among them makes T rune (int32).
	r := pick(1, 'a')
	r += 2147483647
	fmt.Println(r)
	b := mapBox(box[int]{3}, func(x int) string { return fmt.Sprint("n=", x) })
	fmt.Println(b.v)
	h := compose(func(x int) int { return x + 1 }, func(y int) string { return fmt.Sprint(y * 2) })
	fmt.Println(h(4))
	fmt.Println(fold([]int{1, 2, 3}, "", func(acc string, x int) string { return acc + fmt.Sprint(x) }))
	fmt.Println(convert[string](7, func(x int) string { return fmt.Sprint(x, "!") }))
}
