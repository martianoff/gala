// Package go_tilde_constraint_bridge provides generic Go functions whose
// slice and map parameters have type-set constraints (`S ~[]E`,
// `M ~map[K]V`), to verify that GALA infers E, K and V from them.
package go_tilde_constraint_bridge

import "iter"

// FindPlain is the plain form: E is bound by the []E parameter directly.
func FindPlain[E any](s []E, f func(E) bool) int {
	for i, x := range s {
		if f(x) {
			return i
		}
	}
	return -1
}

// FindTilde is the type-set form: E is bound through S's constraint.
func FindTilde[S ~[]E, E any](s S, f func(E) bool) int {
	for i, x := range s {
		if f(x) {
			return i
		}
	}
	return -1
}

// CountWhere binds K and V through M's constraint.
func CountWhere[M ~map[K]V, K comparable, V any](m M, f func(K, V) bool) int {
	n := 0
	for k, v := range m {
		if f(k, v) {
			n++
		}
	}
	return n
}

// Nums is a Go type defined over []int.
type Nums []int

// MakeNums returns a Nums.
func MakeNums() Nums { return Nums{5, 1, 4} }

// Scores is a Go type defined over map[string]int.
type Scores map[string]int

// MakeScores returns a Scores.
func MakeScores() Scores { return Scores{"ann": 3, "bob": 1} }

// Result is a generic struct: its fields are read through an instance.
type Result[T any] struct {
	Err   error
	Value T
}

// Load returns a Result[int].
func Load() Result[int] { return Result[int]{Value: 7} }

// Bag is a generic slice type with an iterator method.
type Bag[T any] []T

// All iterates over the bag's elements.
func (b Bag[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, x := range b {
			if !yield(x) {
				return
			}
		}
	}
}

// MakeBag returns a Bag[string].
func MakeBag() Bag[string] { return Bag[string]{"p", "q"} }

// Stream is a generic struct with an iterator field.
type Stream[T any] struct {
	Next iter.Seq[T]
}

// LoadStream returns a Stream[string].
func LoadStream() Stream[string] {
	return Stream[string]{Next: MakeBag().All()}
}

// Pairs is an iterator over a Scores, sorted by name.
func Pairs(s Scores) iter.Seq2[string, int] {
	return func(yield func(string, int) bool) {
		for _, k := range []string{"ann", "bob"} {
			if v, ok := s[k]; ok && !yield(k, v) {
				return
			}
		}
	}
}
