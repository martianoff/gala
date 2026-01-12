package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Compile-time interface compliance checks for Unapply interface
// These verify that option.gala and either.gala's Unapply methods implement the Unapply interface

// Some implements Unapply[any, any] - extracts value from Option
var _ Unapply[any, any] = Some{}

// None implements Unapply[any, bool] - returns true if value is None/nil
var _ Unapply[any, bool] = None{}

// Left implements Unapply[any, any] - extracts left value from Either
var _ Unapply[any, any] = Left{}

// Right implements Unapply[any, any] - extracts right value from Either
var _ Unapply[any, any] = Right{}

func TestUnapplyCheck(t *testing.T) {
	// 1. Basic equality
	assert.True(t, UnapplyCheck(10, 10))
	assert.False(t, UnapplyCheck(10, 20))
	assert.True(t, UnapplyCheck("hello", "hello"))

	// 2. Immutable
	imm := NewImmutable(10)
	assert.True(t, UnapplyCheck(imm, 10))
	assert.False(t, UnapplyCheck(imm, 20))
}
