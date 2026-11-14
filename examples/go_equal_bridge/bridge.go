// Package go_equal_bridge provides Go types for testing how GALA's == compares
// a Go type that declares an Equal method, and type parameters constrained by
// an interface that embeds comparable.
package go_equal_bridge

// Version is equal to another Version when their numbers are; Label is a
// note that Equal ignores, so Go's == and Equal disagree on it.
type Version struct {
	Major int
	Minor int
	Label string
}

// Equal reports whether v and o have the same numbers.
func (v Version) Equal(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor
}

// Loose declares Equal on its pointer only, so a Loose value has no Equal of
// its own: == compares it with Go's ==, in generic code as well.
type Loose struct {
	N     int
	Label string
}

// Equal reports whether l and o have the same number.
func (l *Loose) Equal(o Loose) bool {
	return l.N == o.N
}

// Key is a constraint that embeds comparable.
type Key interface {
	comparable
}

// Named is a constraint that embeds comparable through Key, and has a method.
type Named interface {
	Key
	Name() string
}
