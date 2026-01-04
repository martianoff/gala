package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptionImplementation(t *testing.T) {
	t.Run("Some", func(t *testing.T) {
		o := Some(10)
		assert.True(t, o.IsDefined())
		assert.False(t, o.IsEmpty())
		assert.Equal(t, 10, o.Get())
		assert.Equal(t, 10, o.GetOrElse(20))
	})

	t.Run("None", func(t *testing.T) {
		o := None[int]()
		assert.False(t, o.IsDefined())
		assert.True(t, o.IsEmpty())
		assert.Panics(t, func() { o.Get() })
		assert.Equal(t, 20, o.GetOrElse(20))
	})

	t.Run("Map", func(t *testing.T) {
		o := Some(10)
		m := Option_Map(o, func(v int) string { return "val" })
		assert.True(t, m.IsDefined())
		assert.Equal(t, "val", m.Get())

		n := None[int]()
		nm := Option_Map(n, func(v int) string { return "val" })
		assert.True(t, nm.IsEmpty())
	})

	t.Run("FlatMap", func(t *testing.T) {
		o := Some(10)
		m := Option_FlatMap(o, func(v int) Option[string] { return Some("val") })
		assert.True(t, m.IsDefined())
		assert.Equal(t, "val", m.Get())

		nm := Option_FlatMap(o, func(v int) Option[string] { return None[string]() })
		assert.True(t, nm.IsEmpty())
	})

	t.Run("Filter", func(t *testing.T) {
		o := Some(10)
		assert.True(t, o.Filter(func(v int) bool { return v > 5 }).IsDefined())
		assert.True(t, o.Filter(func(v int) bool { return v > 15 }).IsEmpty())
	})

	t.Run("ForEach", func(t *testing.T) {
		count := 0
		Some(10).ForEach(func(v int) any {
			count += v
			return nil
		})
		assert.Equal(t, 10, count)

		None[int]().ForEach(func(v int) any {
			count += v
			return nil
		})
		assert.Equal(t, 10, count)
	})

	t.Run("Unapply", func(t *testing.T) {
		assert.True(t, Some(10).Unapply(10).IsDefined())
		assert.False(t, Some(10).Unapply(20).IsDefined())
		assert.False(t, None[int]().Unapply(10).IsDefined())
		// Test matching against None() itself?
		assert.False(t, None[int]().Unapply(None[int]()).IsDefined())
	})
}
