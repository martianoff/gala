package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptionImplementation(t *testing.T) {
	t.Run("Some", func(t *testing.T) {
		o := Some_Apply(Some{}, 10)
		assert.True(t, o.IsDefined())
		assert.False(t, o.IsEmpty())
		assert.Equal(t, 10, o.Get())
		assert.Equal(t, 10, o.GetOrElse(20))
	})

	t.Run("None", func(t *testing.T) {
		o := None_Apply[int](None{})
		assert.False(t, o.IsDefined())
		assert.True(t, o.IsEmpty())
		assert.Panics(t, func() { o.Get() })
		assert.Equal(t, 20, o.GetOrElse(20))
	})

	t.Run("Map", func(t *testing.T) {
		o := Some_Apply(Some{}, 10)
		m := Option_Map[string, int](o, func(v int) string { return "val" })
		assert.True(t, m.IsDefined())
		assert.Equal(t, "val", m.Get())

		n := None_Apply[int](None{})
		nm := Option_Map[string, int](n, func(v int) string { return "val" })
		assert.True(t, nm.IsEmpty())
	})

	t.Run("FlatMap", func(t *testing.T) {
		o := Some_Apply(Some{}, 10)
		m := Option_FlatMap[string, int](o, func(v int) Option[string] { return Some_Apply(Some{}, "val") })
		assert.True(t, m.IsDefined())
		assert.Equal(t, "val", m.Get())

		nm := Option_FlatMap[string, int](o, func(v int) Option[string] { return None_Apply[string](None{}) })
		assert.True(t, nm.IsEmpty())
	})

	t.Run("Filter", func(t *testing.T) {
		o := Some_Apply(Some{}, 10)
		assert.True(t, o.Filter(func(v int) bool { return v > 5 }).IsDefined())
		assert.True(t, o.Filter(func(v int) bool { return v > 15 }).IsEmpty())
	})

	t.Run("ForEach", func(t *testing.T) {
		count := 0
		Some_Apply(Some{}, 10).ForEach(func(v int) any {
			count += v
			return nil
		})
		assert.Equal(t, 10, count)

		None_Apply[int](None{}).ForEach(func(v int) any {
			count += v
			return nil
		})
		assert.Equal(t, 10, count)
	})

	t.Run("IsDefined_GetSomeValue", func(t *testing.T) {
		assert.True(t, IsDefined(true))
		assert.False(t, IsDefined(false))
		assert.False(t, IsDefined(nil))
		assert.False(t, IsDefined(10))

		o := Some_Apply(Some{}, 10)
		assert.True(t, IsDefined(o))
		assert.Equal(t, 10, GetSomeValue(o))

		n := None_Apply[int](None{})
		assert.False(t, IsDefined(n))
		assert.Equal(t, 0, GetSomeValue(n))

		assert.Equal(t, "hello", GetSomeValue("hello"))
		assert.Equal(t, nil, GetSomeValue(nil))
	})
}
