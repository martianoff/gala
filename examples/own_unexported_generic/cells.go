package own_unexported_generic

// cell is an unexported generic struct of the package's own Go code.
type cell[T any] struct{ v T }

// visit calls f with a cell holding n.
func visit(n int, f func(*cell[int]) int) int { return f(&cell[int]{v: n}) }

// pair calls f with the pair n, n+1.
func pair(n int, f func([2]int) int) int { return f([2]int{n, n + 1}) }

// value reads a cell.
func value(c *cell[int]) int { return c.v }
