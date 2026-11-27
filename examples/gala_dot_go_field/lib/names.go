package lib

// Bare is a Go map type of a hand-written Go file of the GALA package lib.
type Bare map[string]int

// NewBare is a Bare of two entries.
func NewBare() Bare { return Bare{"a": 1, "b": 2} }
