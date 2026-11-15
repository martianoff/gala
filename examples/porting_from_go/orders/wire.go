package orders

// This hand-written Go file shares package orders with the .gala files.
// docs/PORTING_FROM_GO.MD quotes it.

// WireOrder keeps its struct tags, so it stays in Go: GALA has no struct
// tags (GALA-E0085). The .gala files of the package use it like any Go type.
type WireOrder struct {
	ID    string `json:"id"`
	Lines int    `json:"line_count,omitempty"`
}

// LineLimit reads the GALA package-level val MaxLines. Go sees a val as a
// std.Immutable and reads it with Get(); a var would be a plain variable.
func LineLimit() int {
	return MaxLines.Get()
}

// EnableVerbose sets the GALA package-level var Verbose, a plain Go variable.
func EnableVerbose() {
	Verbose = true
}
