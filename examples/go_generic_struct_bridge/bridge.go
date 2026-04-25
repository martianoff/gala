// Package go_generic_struct_bridge provides Go-only generic struct types
// used to verify GALA named-argument construction works for generic
// Go-style structs declared in a dot-imported package.
package go_generic_struct_bridge

import "fmt"

// Box is a non-generic Go-style struct used as a control to confirm the
// non-generic dot-import named-arg path works.
type Box struct {
	Title string
}

// Render returns a human-readable representation of a Box.
func (b Box) Render() string {
	return fmt.Sprintf("Box(%s)", b.Title)
}

// Container is a single-type-parameter generic Go-style struct. The GALA
// transpiler must accept named-argument construction `Container[int](Value = 42)`
// from a dot-importing consumer.
type Container[A any] struct {
	Value A
}

// Render returns a human-readable representation of a Container.
func (c Container[A]) Render() string {
	return fmt.Sprintf("Container(%v)", c.Value)
}

// Pair is a two-type-parameter generic Go-style struct. The GALA transpiler
// must accept `Pair[string, int](First = ..., Second = ...)` (an
// *ast.IndexListExpr call target) from a dot-importing consumer.
type Pair[A any, B any] struct {
	First  A
	Second B
}

// Render returns a human-readable representation of a Pair.
func (p Pair[A, B]) Render() string {
	return fmt.Sprintf("Pair(%v, %v)", p.First, p.Second)
}
