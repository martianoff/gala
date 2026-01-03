package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnapplyCheck(t *testing.T) {
	// 1. Basic equality
	assert.True(t, UnapplyCheck(10, 10))
	assert.False(t, UnapplyCheck(10, 20))
	assert.True(t, UnapplyCheck("hello", "hello"))

	// 2. Immutable
	imm := NewImmutable(10)
	assert.True(t, UnapplyCheck(imm, 10))
	assert.False(t, UnapplyCheck(imm, 20))

	// 3. Custom types with Unapply
	assert.True(t, UnapplyCheck(Int(10), 10))
	assert.True(t, UnapplyCheck(String("hello"), "hello"))
}
