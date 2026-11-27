// Package go_exact_signature_bridge provides Go functions and a struct
// whose parameter, result and field types name a generic struct instance or
// a fixed-length array.
package go_exact_signature_bridge

// Box holds a value.
type Box[T any] struct{ V T }

// Take calls f with a box of 1.
func Take(f func(*Box[int]) int) int { return f(&Box[int]{V: 1}) }

// SumPair calls f with the pair 2, 3.
func SumPair(f func([2]int) int) int { return f([2]int{2, 3}) }

// First returns a box of 7.
func First() *Box[int] { return &Box[int]{V: 7} }

// Handlers holds a callback over a box.
type Handlers struct {
	OnBox func(*Box[int]) int
}

// Run calls the callback with a box of 4.
func (h Handlers) Run() int { return h.OnBox(&Box[int]{V: 4}) }

// DefaultBox is a package variable of a generic struct instance.
var DefaultBox = &Box[int]{V: 3}

// Maker makes a box.
type Maker interface{ Make() *Box[int] }

// UseMaker reads the box m makes.
func UseMaker(m Maker) int { return m.Make().V }

// Iter yields values.
type Iter[T any] interface{ Next() T }

type intIter struct{}

func (intIter) Next() int { return 9 }

// Ints returns an Iter[int].
func Ints() Iter[int] { return intIter{} }

// Inner holds a value.
type Inner[U any] struct{ v U }

// Get returns the value.
func (i *Inner[U]) Get() U { return i.v }

// Len reports a length.
func (i *Inner[U]) Len() int { return 2 }

// Outer embeds an Inner of its first type parameter.
type Outer[A, B any] struct {
	Inner[A]
	b B
}

// NewOuter returns an Outer whose Inner holds 5.
func NewOuter() *Outer[int, string] { return &Outer[int, string]{Inner: Inner[int]{v: 5}} }
