package std

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

type caseless string

// Equal makes a named string type compare differently from ==, so the
// primitive fast path must leave it alone.
func (c caseless) Equal(o caseless) bool { return len(c) == len(o) }

// TestEqualPrimitives guards the reflection-free path for predeclared
// primitive types: it must agree with what Equal's reflective path decides.
func TestEqualPrimitives(t *testing.T) {
	nan := math.NaN()
	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"equal ints", Equal(3, 3), true},
		{"different ints", Equal(3, 4), false},
		{"equal strings", Equal("a", "a"), true},
		{"NaN is not equal to itself", Equal(nan, nan), false},
		{"signed zeros are equal", Equal(0.0, math.Copysign(0, -1)), true},
		{"different dynamic types differ", Equal[any](1, int64(1)), false},
		{"primitive and non-primitive differ", Equal[any](1, []int{1}), false},
		{"named type keeps its own Equal", Equal(caseless("ab"), caseless("xy")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}

// node is comparable, but == compares where next points, not what it holds,
// as with an immutable List; its Equal compares contents.
type node struct {
	v    int
	next *node
}

func (n node) Equal(o node) bool {
	return n.v == o.v && (n.next == nil) == (o.next == nil) && (n.next == nil || n.next.Equal(*o.next))
}

// boxed is comparable as a type, but == panics when it holds a slice.
type boxed struct{ v any }

func (b boxed) Equal(o boxed) bool { return Equal(b.v, o.v) }

// TestKeyEqual checks that KeyEqual decides as Equal does, through its ==
// shortcut and around it.
func TestKeyEqual(t *testing.T) {
	nan := math.NaN()
	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"equal ints", KeyEqual(3, 3), true},
		{"different strings", KeyEqual("a", "b"), false},
		{"NaN is not equal to itself", KeyEqual(nan, nan), false},
		{"named type keeps its own Equal", KeyEqual(caseless("ab"), caseless("xy")), true},
		{"== tuples", KeyEqual(Tuple[int, string]{NewImmutable(1), NewImmutable("a")}, Tuple[int, string]{NewImmutable(1), NewImmutable("a")}), true},
		{"different tuples", KeyEqual(Tuple[int, string]{NewImmutable(1), NewImmutable("a")}, Tuple[int, string]{NewImmutable(1), NewImmutable("b")}), false},
		{"not == but Equal", KeyEqual(node{1, &node{2, nil}}, node{1, &node{2, nil}}), true},
		{"neither == nor Equal", KeyEqual(node{1, &node{2, nil}}, node{1, &node{3, nil}}), false},
		{"== would panic, Equal", KeyEqual(boxed{[]int{1}}, boxed{[]int{1}}), true},
		{"== would panic, not Equal", KeyEqual(boxed{[]int{1}}, boxed{[]int{2}}), false},
		{"interface type, different dynamic types", KeyEqual[any](1, int64(1)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}
