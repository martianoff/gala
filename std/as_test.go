package std

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

type TestStruct struct {
	Name string
}

type GenericStruct[T any] struct {
	Value T
}

type Shaper interface {
	Area() float64
}

type Rect struct {
	Width, Height float64
}

func (r Rect) Area() float64 {
	return r.Width * r.Height
}

func TestAs(t *testing.T) {
	// 1. Structures
	t.Run("Structures", func(t *testing.T) {
		s := TestStruct{Name: "Gala"}
		val, ok := As[TestStruct](s)
		assert.True(t, ok)
		assert.Equal(t, s, val)

		val2, ok := As[string](s)
		assert.False(t, ok)
		assert.Equal(t, "", val2)
	})

	// 2. Immutable structures
	t.Run("Immutable structures", func(t *testing.T) {
		s := TestStruct{Name: "Gala"}
		imm := NewImmutable(s)
		val, ok := As[TestStruct](imm)
		assert.True(t, ok)
		assert.Equal(t, s, val)
	})

	// 3. Generics
	t.Run("Generics", func(t *testing.T) {
		gs := GenericStruct[int]{Value: 42}
		var ok bool
		val, ok := As[GenericStruct[int]](gs)
		assert.True(t, ok)
		assert.Equal(t, gs, val)

		_, ok = As[GenericStruct[string]](gs)
		assert.False(t, ok)
	})

	// 4. Wrapped generics
	t.Run("Wrapped generics", func(t *testing.T) {
		gs := GenericStruct[int]{Value: 42}
		imm := NewImmutable(gs)
		val, ok := As[GenericStruct[int]](imm)
		assert.True(t, ok)
		assert.Equal(t, gs, val)
	})

	// 5. Nil case
	t.Run("Nil", func(t *testing.T) {
		val, ok := As[TestStruct](nil)
		assert.False(t, ok)
		assert.Equal(t, TestStruct{}, val)
	})

	// 6. Interfaces
	t.Run("Interfaces", func(t *testing.T) {
		r := Rect{Width: 10, Height: 5}
		val, ok := As[Shaper](r)
		assert.True(t, ok)
		assert.Equal(t, r, val)
	})
}
