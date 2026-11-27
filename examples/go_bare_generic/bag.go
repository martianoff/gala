package go_bare_generic

// Bag is a generic Go slice type of a hand-written Go file of the package.
type Bag[T any] []T

// Pair is a generic Go map type.
type Pair[K comparable, V any] map[K]V

// Box is a generic Go slice type GALA gives a method (bags.gala).
type Box[T any] []T

// NewBox is a Box of xs.
func NewBox[T any](xs ...T) Box[T] { return xs }
