package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A method that uses a type parameter of its type where Go requires a
// comparable type, while the type declares it `any`, is lowered to a function
// whose copy of the parameter is constrained comparable. Its GALA form stays a
// method: a call, from the same package or another, calls the function, a
// method value becomes a function value, and a call on a receiver whose type
// argument Go cannot compare is GALA-E0086.

const comparableBagLib = `package lib

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/go_interop"
)

struct Bag[K any, V any](keys Array[K], vals Array[V])

func NewBag[K any, V any](keys Array[K], vals Array[V]) Bag[K, V] = Bag(keys, vals)

func (b Bag[K, V]) Index() map[K]int {
    var out = go_interop.MapEmpty[K, int]()
    b.keys.ZipWithIndex().ForEach((t) => go_interop.MapPut(out, t.V1, t.V2))
    return out
}

func (b Bag[K, V]) Has(k K) bool = b.keys.Exists((x) => x == k)

func (b Bag[K, V]) Count() int = b.Index().Size()

func (b Bag[K, V]) Size() int = b.keys.Size()
`

func comparableBagFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	write("gala.mod", "module example.com/cmpx\n\ngala dev\n")
	write("lib/lib.gala", comparableBagLib)
	return root
}

func TestComparableReceiverParamLowering(t *testing.T) {
	root := comparableBagFixture(t)
	tests := []struct {
		name, src     string
		contains      []string
		notContains   []string
		wantErrCode   galaerr.ErrorCode
		wantErrSubstr string
	}{
		{
			name: "declaration in its own package",
			src:  comparableBagLib,
			contains: []string{
				"func Bag_Index[K comparable, V any](b Bag[K, V]) map[K]int",
				"func Bag_Has[K comparable, V any](b Bag[K, V], k K) bool",
				"func Bag_Count[K comparable, V any](b Bag[K, V]) int",
				"len(Bag_Index[K, V](b))",
				"func (b Bag[K, V]) Size() int",
			},
		},
		{
			name: "calls from another package",
			src: `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/cmpx/lib"
)

func main() {
    val b = lib.NewBag(ArrayOf("a"), ArrayOf(1))
    Println(b.Index(), b.Has("a"), b.Size())
}
`,
			contains:    []string{"lib.Bag_Index[string, int](b)", `lib.Bag_Has[string, int](b, "a")`, "b.Size()"},
			notContains: []string{"b.Index()"},
		},
		{
			name: "method value from another package",
			src: `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/cmpx/lib"
)

func main() {
    val b = lib.NewBag(ArrayOf("a"), ArrayOf(1))
    val index = b.Index
    val has = b.Has
    Println(index(), has("a"))
}
`,
			contains: []string{"return lib.Bag_Index[string, int](", "return lib.Bag_Has[string, int]("},
		},
		{
			name: "== on a value of the receiver's type parameter",
			src: `package main

struct Box[T any](v T)

func (b Box[T]) Holds(x T) bool = b.v == x

func main() { Println(Box(1).Holds(1)) }
`,
			contains: []string{"func Box_Holds[T comparable](b Box[T], x T) bool", "Box_Holds[int](Box[int]{"},
		},
		{
			name: "HashMap.ToGoMap on a comparable key",
			src: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    Println(HashMapOf(("a", 1)).ToGoMap())
}
`,
			contains: []string{"HashMap_ToGoMap[string, int]("},
		},
		{
			name: "non-comparable type argument from another package",
			src: `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/cmpx/lib"
)

func main() {
    val b = lib.NewBag(ArrayOf(ArrayOf(1)), ArrayOf(1))
    Println(b.Index())
}
`,
			wantErrCode:   galaerr.CodeIncomparableTypeArgument,
			wantErrSubstr: "Bag.Index needs a comparable K: Array[int] holds a Go slice",
		},
		{
			name: "non-comparable key on HashMap.ToGoMap",
			src: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    Println(HashMapOf((ListOf(func() {}), 1)).ToGoMap())
}
`,
			wantErrCode:   galaerr.CodeIncomparableTypeArgument,
			wantErrSubstr: "HashMap.ToGoMap needs a comparable K",
		},
		{
			name: "method value on a non-comparable type argument",
			src: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val toGo = HashMapOf((ArrayOf(1), 1)).ToGoMap
    Println(toGo())
}
`,
			wantErrCode:   galaerr.CodeIncomparableTypeArgument,
			wantErrSubstr: "HashMap.ToGoMap needs a comparable K",
		},
		{
			name: "a type parameter constrained by any",
			src: `package main

import . "martianoff/gala/collection_immutable"

func toGo[K any, V any](m HashMap[K, V]) int = m.ToGoMap().Size()

func main() {
    Println(toGo(HashMapOf(("a", 1))))
}
`,
			wantErrCode:   galaerr.CodeIncomparableTypeArgument,
			wantErrSubstr: "HashMap.ToGoMap needs a comparable K: K is a type parameter constrained by any",
		},
		{
			name: "a type parameter constrained comparable",
			src: `package main

import . "martianoff/gala/collection_immutable"

func toGo[K comparable, V any](m HashMap[K, V]) int = m.ToGoMap().Size()

func main() {
    Println(toGo(HashMapOf(("a", 1))))
}
`,
			contains: []string{"HashMap_ToGoMap[K, V](m)"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, "main.gala")
			if tc.src == comparableBagLib {
				path = filepath.Join(root, "lib", "lib.gala")
			}
			out, err := transpileCrossPkgFile(t, root, tc.src, path)
			if tc.wantErrCode != "" {
				require.Error(t, err)
				var se *galaerr.SemanticError
				require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
				assert.Equal(t, tc.wantErrCode, se.Code)
				assert.Contains(t, se.Error(), tc.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			for _, s := range tc.contains {
				assert.Contains(t, out, s, "generated:\n%s", out)
			}
			for _, s := range tc.notContains {
				assert.NotContains(t, out, s, "generated:\n%s", out)
			}
		})
	}
}
