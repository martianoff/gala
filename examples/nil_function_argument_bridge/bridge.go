// Package nil_function_argument_bridge declares Go types no nil can be,
// though GALA cannot spell them: a named fixed-length array, aliases of a
// struct type and of the array (and, for contrast, an alias of a pointer),
// and Go functions taking a `func() struct{}`
// and a `func() (int, error)`.
package nil_function_argument_bridge

import "time"

// ID is a named array, as uuid.UUID is.
type ID [16]byte

// IDAlias is an alias of the named array.
type IDAlias = ID

// Stamp is an alias of a struct type.
type Stamp = time.Time

// StampRef is an alias of a pointer, which nil can be.
type StampRef = *time.Time

// Run is n when f is nil, else 0.
func Run(f func() struct{}, n int) int {
	if f == nil {
		return n
	}
	return 0
}

// Pair is n when f is nil, else the value f gives.
func Pair(f func() (int, error), n int) int {
	if f == nil {
		return n
	}
	v, _ := f()
	return v
}
