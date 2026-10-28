package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

const comparableTypes = `package bag

import . "martianoff/gala/collection_immutable"

struct Bag[K any, V any](keys Array[K], vals Array[V])

struct Link[K any](var next OptionPtr[Link[K]], key K)
`

const comparableMethods = `package bag

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/go_interop"
)

func (b Bag[K, V]) Index() map[K]int = go_interop.MapEmpty[K, int]()

func (b Bag[K, V]) ByValue() map[V]K = go_interop.MapEmpty[V, K]()

func (b Bag[K, V]) Merge(ms []map[K]V) int = ms.Size()

func (b Bag[K, V]) Apply(f func(map[K]V) int) int = 0

func (b Bag[K, V]) Has(k K) bool = b.keys.Exists((x) => x == k)

func (b Bag[K, V]) Differs(v V, w V) bool = v != w

func (b Bag[K, V]) Explicit() int = go_interop.MapEmpty[K, V]().Size()

func (b Bag[K, V]) Inferred(m map[int]V, k int) Option[V] = go_interop.OptionFromMap(m, k)

func (b Bag[K, V]) InferredKey(k K) Option[V] = go_interop.OptionFromMap(b.Pairs(), k)

func (b Bag[K, V]) Pairs() map[K]V = go_interop.MapEmpty[K, V]()

func (b Bag[K, V]) Count() int = b.Index().Size()

func (b Bag[K, V]) Sorted() TreeSet[K] = EmptyTreeSet[K]()

func (b Bag[K, V]) Size() int = b.keys.Size()

func (b Bag[K, V]) Pointer() map[*K]int = go_interop.MapEmpty[*K, int]()

func (l Link[K]) Same(other Link[K]) bool = l.next == other.next

func (l Link[K]) SameKey(other Link[K]) bool = l.key == other.key

func (b Bag[A, B]) Renamed() map[B]int = go_interop.MapEmpty[B, int]()
`

// TestComparableReceiverParams pins which receiver type parameters a method
// needs comparable, as the type names them: a map key in the signature or the
// body, nested or not; an operand of == or != whose declared type holds one; a
// written or inferred type argument of a comparable type parameter, of a Go
// function or a GALA type; and a call of another such method. A pointer, and a
// type that holds the parameter only behind one, need nothing. Each such
// method is lowered to a function (IsGeneric); the others are not.
func TestComparableReceiverParams(t *testing.T) {
	cases := []struct {
		typ, method string
		want        []string
	}{
		{"bag.Bag", "Index", []string{"K"}},
		{"bag.Bag", "ByValue", []string{"V"}},
		{"bag.Bag", "Merge", []string{"K"}},
		{"bag.Bag", "Apply", []string{"K"}},
		{"bag.Bag", "Has", []string{"K"}},
		{"bag.Bag", "Differs", []string{"V"}},
		{"bag.Bag", "Explicit", []string{"K"}},
		{"bag.Bag", "Inferred", nil},
		{"bag.Bag", "InferredKey", []string{"K"}},
		{"bag.Bag", "Count", []string{"K"}},
		{"bag.Bag", "Sorted", []string{"K"}},
		{"bag.Bag", "Size", nil},
		{"bag.Bag", "Pointer", nil},
		{"bag.Link", "Same", nil},
		{"bag.Link", "SameKey", []string{"K"}},
		{"bag.Bag", "Renamed", []string{"V"}},
	}
	for _, analyzed := range []string{"methods.gala", "types.gala"} {
		rich := analyzeBagPackage(t, analyzed)
		for _, tc := range cases {
			t.Run(analyzed+"/"+tc.method, func(t *testing.T) {
				meta := rich.Types[tc.typ]
				require.NotNil(t, meta, tc.typ)
				m := meta.Methods[tc.method]
				require.NotNil(t, m, tc.method)
				assert.Equal(t, tc.want, m.ComparableTypeParams)
				assert.Equal(t, tc.want != nil, m.IsGeneric, "lowered to a function")
			})
		}
	}
}

func analyzeBagPackage(t *testing.T, analyzed string) *transpiler.RichAST {
	t.Helper()
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "gala.mod"), []byte("module example.com/bag\n\ngala dev\n"), 0o644))
	files := map[string]string{"types.gala": comparableTypes, "methods.gala": comparableMethods}
	var sibling string
	for name, src := range files {
		require.NoError(t, os.WriteFile(filepath.Join(tmp, name), []byte(src), 0o644))
		if name != analyzed {
			sibling = filepath.Join(tmp, name)
		}
	}
	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, append([]string{tmp}, getStdSearchPath()...), tmp)
	tree, _, err := p.Parse(files[analyzed])
	require.NoError(t, err)
	batch.SetPackageFiles([]string{sibling})
	rich, err := batch.Analyze(tree, nil, filepath.Join(tmp, analyzed))
	require.NoError(t, err)
	return rich
}
