// Package qa_lib_bridge provides Go-only struct types — one generic and one
// non-generic — used to verify that a dot-import is preserved when the
// consumer references only generic instantiations.
package qa_lib_bridge

import "fmt"

// Single is a single-type-parameter generic Go-style struct used to exercise
// the dot-import-used scan against an *ast.IndexExpr-shaped reference.
type Single[T any] struct {
	Name string
	Item T
}

// Render returns a human-readable representation of a Single.
func (s Single[T]) Render() string {
	return fmt.Sprintf("%s=%v", s.Name, s.Item)
}

// Plain is a non-generic control type. Used only to confirm the
// scanner already handles the bare-Ident anchor case.
type Plain struct {
	Tag string
}

// Render returns a human-readable representation of a Plain.
func (p Plain) Render() string {
	return fmt.Sprintf("Plain(%s)", p.Tag)
}
