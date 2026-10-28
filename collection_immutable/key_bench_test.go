package collection_immutable

// Lookup benchmarks of HashSet and HashMap by key type: the key comparison
// (std.KeyEqual) runs on every lookup.

import (
	"strconv"
	"testing"

	"martianoff/gala/std"
)

// plainKey is a Go struct key with a Hash method and no Equal: it compares
// with ==.
type plainKey struct{ a, b int }

func (k plainKey) Hash() uint32 {
	return std.HashCombine(std.HashInt(int64(k.a)), std.HashInt(int64(k.b)))
}

func keysOf[K any](f func(int) K) []K {
	keys := make([]K, benchSize)
	for i, k := range benchKeys() {
		keys[i] = f(k)
	}
	return keys
}

func benchSetContains[K any](b *testing.B, keys []K) {
	s := EmptyHashSet[K]()
	for _, k := range keys {
		s = s.Add(k)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = s.Contains(k)
		}
	}
}

func benchMapGet[K comparable](b *testing.B, keys []K) {
	m := EmptyHashMap[K, int]()
	for i, k := range keys {
		m = m.Put(k, i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = m.Contains(k)
		}
	}
}

var (
	intKeys    = keysOf(func(i int) int { return i })
	stringKeys = keysOf(func(i int) string { return "key-" + strconv.Itoa(i) })
	tupleKeys  = keysOf(func(i int) std.Tuple[int, int] {
		return std.Tuple[int, int]{V1: std.NewImmutable(i), V2: std.NewImmutable(i * 3)}
	})
	optionKeys = keysOf(func(i int) std.Option[int] { return std.NewOptionSome(i) })
	plainKeys  = keysOf(func(i int) plainKey { return plainKey{i, i * 3} })
)

func BenchmarkKeySetInt(b *testing.B)    { benchSetContains(b, intKeys) }
func BenchmarkKeySetString(b *testing.B) { benchSetContains(b, stringKeys) }
func BenchmarkKeySetTuple(b *testing.B)  { benchSetContains(b, tupleKeys) }
func BenchmarkKeySetOption(b *testing.B) { benchSetContains(b, optionKeys) }
func BenchmarkKeySetPlain(b *testing.B)  { benchSetContains(b, plainKeys) }
func BenchmarkKeyMapInt(b *testing.B)    { benchMapGet(b, intKeys) }
func BenchmarkKeyMapString(b *testing.B) { benchMapGet(b, stringKeys) }
func BenchmarkKeyMapTuple(b *testing.B)  { benchMapGet(b, tupleKeys) }
func BenchmarkKeyMapOption(b *testing.B) { benchMapGet(b, optionKeys) }
func BenchmarkKeyMapPlain(b *testing.B)  { benchMapGet(b, plainKeys) }
