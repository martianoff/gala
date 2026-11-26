package go_bare_named_size

// Names, Counts, Sized and Score are declared in a hand-written Go file of
// the package. GALA code of the same package writes them bare (see
// sizes.gala).
type Names []string

type Counts map[string]int

// Sized has a Size method of its own, which .Size() calls.
type Sized []int

func (s Sized) Size() int { return 100 + len(s) }

// Tagged gets a Size method from GALA (sizes.gala), which .Size() calls.
type Tagged []string

func NewTagged(xs ...string) Tagged { return xs }

// Score is a numeric type, which an untyped constant fills.
type Score float64
