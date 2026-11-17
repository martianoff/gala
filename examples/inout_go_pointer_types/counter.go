package inout_go_pointer_types

// GoCounter is a struct a Go file declares.
type GoCounter struct{ N int }

// Bump is a pointer-receiver method: it changes the counter it is called on.
func (c *GoCounter) Bump() { c.N++ }

// Pair holds a counter by value.
type Pair struct{ C GoCounter }

// A is a generic Go alias of a pointer.
type A[T any] = *T

// NP is a Go defined type whose underlying type is a pointer.
type NP *GoCounter

// PT is a generic Go defined type of a pointer.
type PT[T any] *T

// PP is a Go defined pointer type to a struct holding a counter.
type PP *Pair

// IDs is a Go defined slice type.
type IDs []int

// NewP returns a fresh counter.
func NewP(n int) *GoCounter { return &GoCounter{N: n} }

// NewPair returns a fresh pair.
func NewPair(n int) *Pair { return &Pair{C: GoCounter{N: n}} }

// NewIDs returns a plain slice, which an IDs slot takes.
func NewIDs() []int { return []int{1, 2, 3} }
