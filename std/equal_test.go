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
