// Package go_generic_alias_bridge provides generic Go type aliases of
// function types, which GALA code spells with type arguments.
package go_generic_alias_bridge

// Handler is a generic alias of a function type.
type Handler[T any] = func(T) error

// Seq is a generic alias of an iterator function type.
type Seq[T any] = func(yield func(T) bool)

// Collect gathers what s yields.
func Collect[T any](s Seq[T]) []T {
	var out []T
	s(func(v T) bool {
		out = append(out, v)
		return true
	})
	return out
}
