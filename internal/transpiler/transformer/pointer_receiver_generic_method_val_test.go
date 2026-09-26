package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generic method with its own type parameter (SortBy[K], Map[U], ...) lowers
// to a standalone `Receiver_Method(recv, ...)` function, and the call's result
// type is recovered by substituting the receiver's type arguments into the
// method's declared return type. For a pointer receiver (every collection_mutable
// type) the receiver argument is `*Array[Row]`: its type arguments must be read
// from the pointee, otherwise the result stays `*Array[T]` and a later call on a
// val bound to it emits `func(r T) any`.
func TestPointerReceiverGenericMethod_ValKeepsElementType(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "aliased mutable SortBy then Map",
			src: `package main

import m "martianoff/gala/collection_mutable"

struct Row(Name string, K int)

func main() {
    val rows = m.ArrayOf(Row("a", 1), Row("b", 0))
    val sorted = rows.SortBy((r) => r.K)
    Println(sorted.Map((r) => r.Name).MkString(","))
}`,
			want: "func(r Row) string",
		},
		{
			name: "dot-imported mutable SortBy then Map",
			src: `package main

import . "martianoff/gala/collection_mutable"

func main() {
    val words = ArrayOf("ccc", "a", "bb")
    val sorted = words.SortBy((w) => w.Size())
    Println(sorted.Map((w) => w + "!").MkString(","))
}`,
			want: "func(w string) string",
		},
		{
			name: "chained mutable HashMap SortBy then Map",
			src: `package main

import m "martianoff/gala/collection_mutable"

func main() {
    val counts = m.HashMapOf(("x", 3), ("y", 1))
    Println(counts.SortBy((e) => e.V2).Map((e) => e.V1).MkString(","))
}`,
			want: "func(e std.Tuple[string, int]) string",
		},
		{
			name: "aliased immutable SortBy then Map",
			src: `package main

import im "martianoff/gala/collection_immutable"

struct Row(Name string, K int)

func main() {
    val rows = im.ArrayOf(Row("a", 1), Row("b", 0))
    val sorted = rows.SortBy((r) => r.K)
    Println(sorted.Map((r) => r.Name).MkString(","))
}`,
			want: "func(r Row) string",
		},
	}

	root := crossPkgFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tc.src)
			require.NoError(t, err)
			assert.NotContains(t, out, " T)", "receiver type parameter leaked into the generated Go:\n%s", out)
			assert.NotContains(t, out, ") any {", "lambda result fell back to any:\n%s", out)
			assert.Contains(t, out, tc.want, "generated:\n%s", out)
		})
	}
}
