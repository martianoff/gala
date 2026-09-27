package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetOnGenericReceiverInsideGenericCode pins the result type of a user
// `Get()` method called on a generic receiver whose type argument is still a
// type parameter — i.e. from inside generic code.
//
// For a pointer receiver (`b *Box[T]`), inference used to skip the method
// lookup because the receiver carried a type parameter, and fell back to
// returning the receiver type itself. `val v = b.Get()` was then typed
// `*Box[T]` instead of `T`, so `Some(v)` emitted `std.Some[*Box[T]]` and the
// generated Go failed to compile. The runtime consequence is pinned by
// examples/get_generic_pointer_receiver.
func TestGetOnGenericReceiverInsideGenericCode(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantContains string
		wantAbsent   string
	}{
		{
			name: "pointer receiver",
			src: `package main

struct Box[T any](Value T)

func (b *Box[T]) Get() T = b.Value

func viaPointer[T any](b *Box[T], fallback T) T {
    val v = b.Get()
    return Some(v).GetOrElse(fallback)
}

func main() {
    var b = Box(41)
    Println(viaPointer(&b, 0))
}`,
			wantContains: "std.Some[T]{}.Apply(v.Get())",
			wantAbsent:   "Some[*Box[T]]",
		},
		{
			name: "value receiver",
			src: `package main

struct Cell[T any](Value T)

func (c Cell[T]) Get() T = c.Value

func viaValue[T any](c Cell[T], fallback T) T {
    val v = c.Get()
    return Some(v).GetOrElse(fallback)
}

func main() {
    Println(viaValue(Cell("x"), "y"))
}`,
			wantContains: "std.Some[T]{}.Apply(v.Get())",
			wantAbsent:   "Some[Cell[T]]",
		},
		{
			name: "pointer receiver with a non-type-parameter result",
			src: `package main

struct Box[T any](Value T)

func (b *Box[T]) Get() Option[T] = Some(b.Value)

func describe[T any](b *Box[T]) string = b.Get().Map((v) => s"holds $v").GetOrElse("empty")

func main() {
    var b = Box(41)
    Println(describe(&b))
}`,
			// Option.Map is a generic method, lowered to a standalone call —
			// only once the receiver is known to be an Option.
			wantContains: "std.Option_Map(b.Get(), func(v T) string",
			wantAbsent:   "b.Get().Map(",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			require.Contains(t, out, tc.wantContains)
			require.NotContains(t, out, tc.wantAbsent,
				"Get() on a generic receiver must not be typed as the receiver itself")
		})
	}
}
