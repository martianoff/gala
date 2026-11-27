package own_unexported_generic

// cell is an unexported generic struct of the package's own Go code.
type cell[T any] struct{ v T }

// visit calls f with a cell holding n.
func visit(n int, f func(*cell[int]) int) int { return f(&cell[int]{v: n}) }

// pair calls f with two cells holding n and n+1.
func pair(n int, f func([2]cell[int]) int) int { return f([2]cell[int]{{v: n}, {v: n + 1}}) }

// at reads the i-th of two cells.
func at(cs [2]cell[int], i int) int { return cs[i].v }

// value reads a cell.
func value(c *cell[int]) int { return c.v }

// Each calls f with a cell holding 5: a method of this file on the GALA type
// Repo.
func (r Repo) Each(f func(*cell[int]) int) int { return f(&cell[int]{v: 5}) }
