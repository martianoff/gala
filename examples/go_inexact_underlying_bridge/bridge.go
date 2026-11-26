// Package go_inexact_underlying_bridge provides generic Go defined function
// types whose signatures name a generic struct instance or a fixed-length
// array.
package go_inexact_underlying_bridge

// Box holds a value.
type Box[T any] struct{ V T }

// WithBox is a generic defined function type over a generic struct instance.
type WithBox[T any] func(*Box[T]) error

// WithPair is a generic defined function type over a fixed-length array.
type WithPair[T any] func([2]T) T

// IntBox is a defined function type over a generic struct instance.
type IntBox func(*Box[int]) error

// Use takes a Box[int].
func Use(b *Box[int]) error { return nil }

// PairOf makes a two-element array.
func PairOf[T any](a, b T) [2]T { return [2]T{a, b} }
