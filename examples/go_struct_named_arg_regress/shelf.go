package go_struct_named_arg_regress

import "fmt"

// Shelf is declared in a hand-written Go file of the package. GALA code of
// the same package builds it with named arguments (see shelf.gala).
type Shelf struct {
	Label string
	Scale func(int) int
	count int
}

func (s Shelf) String() string {
	return fmt.Sprintf("Shelf(%s, %d, %d)", s.Label, s.Scale(4), s.count)
}
