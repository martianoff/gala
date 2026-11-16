// Generics: a generic struct with methods, a generic function whose type
// arguments are inferred, zero values of a type parameter.
package main

import "fmt"

type Box[T any] struct {
	v T
}

func (b Box[T]) Get() T { return b.v }

func (b *Box[T]) Set(v T) { b.v = v }

func Wrap[T any](v T) Box[T] { return Box[T]{v: v} }

func Zero[T any]() T {
	var z T
	return z
}

type Pair[A any, B any] struct {
	First  A
	Second B
}

func main() {
	b := Wrap(3)
	b.Set(b.Get() + 4)
	s := Wrap("s")
	fmt.Println(b.Get(), s.Get(), Zero[int](), Zero[string]() == "", Zero[*int]() == nil)
	fmt.Println(Pair[int, string]{1, "one"}, Box[bool]{true})
}
