// Package anon_struct_field_bridge returns anonymous Go struct types whose
// fields have a fixed-length array type, a generic type instance or an
// inline interface type, so GALA has to spell each field's type exactly.
package anon_struct_field_bridge

// Box is a generic Go struct, named by an anonymous struct's field.
type Box[T any] struct {
	Value T
}

// Square has an Area method, so it satisfies an inline interface type.
type Square struct {
	Side int
}

// Area is the square's area.
func (s Square) Area() int { return s.Side * s.Side }

// Grid is a struct whose field is a fixed-length array.
func Grid() struct {
	Cells [3]int
	Name  string
} {
	return struct {
		Cells [3]int
		Name  string
	}{Cells: [3]int{1, 2, 3}, Name: "grid"}
}

// Boxed is a struct whose field is a generic type instance.
func Boxed() struct{ Inner Box[int] } {
	return struct{ Inner Box[int] }{Inner: Box[int]{Value: 7}}
}

// Shaped is a struct whose field is an inline, non-empty interface type.
func Shaped() struct{ Shape interface{ Area() int } } {
	return struct{ Shape interface{ Area() int } }{Shape: Square{Side: 4}}
}
