// Package go_alias_exact_bridge provides Go type aliases of a generic struct
// instance and of a fixed-length array, used in signatures.
package go_alias_exact_bridge

import "os"

// Box holds a value.
type Box[T any] struct{ V T }

// IntBox is an alias of a generic struct instance.
type IntBox = Box[int]

// Pair is an alias of a fixed-length array.
type Pair = [2]int

// TakeBox calls f with a box of 1.
func TakeBox(f func(*IntBox) int) int { return f(&IntBox{V: 1}) }

// TakePair calls f with the pair 2, 3.
func TakePair(f func(Pair) int) int { return f(Pair{2, 3}) }

// NewBox returns a box of 7.
func NewBox() *IntBox { return &IntBox{V: 7} }

// Modes returns two file modes.
func Modes() [2]os.FileMode { return [2]os.FileMode{0o644, 0o755} }

// RunModes counts the modes f returns: a function type naming a package's
// alias of a named type (os.FileMode = fs.FileMode).
func RunModes(f func() [2]os.FileMode) int { return len(f()) }
