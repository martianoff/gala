package collection_immutable

// Benchmarks of collections used as HashSet elements and HashMap keys: Hash()
// itself and lookups that hash and compare collection keys. The construction
// benchmarks in collections_bench_test.go (ArrayBuild, ArrayPrepend,
// ArrayUpdated, HashSetBuild, ...) show what keeping the hashes costs
// collections that are never hashed. Run with:
//
//	bazel run //collection_immutable:collections_bench -- -test.bench='Hash|Key' -test.benchmem -test.run='^$'

import (
	"testing"
)

var sinkU32 uint32

// keyPool is how many collection keys the lookup benchmarks hold, each of
// keyLen elements.
const (
	keyPool = 256
	keyLen  = 1024
)

func intRange(start, n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = start + i
	}
	return s
}

func arrayKeys() []Array[int] {
	keys := make([]Array[int], keyPool)
	for i := range keys {
		keys[i] = ArrayFromSlice(intRange(i*keyLen, keyLen))
	}
	return keys
}

func setKeys() []HashSet[int] {
	keys := make([]HashSet[int], keyPool)
	for i := range keys {
		keys[i] = HashSetFromSlice(intRange(i*keyLen, keyLen))
	}
	return keys
}

func benchHash(b *testing.B, hash func() uint32) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkU32 = hash()
	}
}

func BenchmarkHashArray(b *testing.B) { benchHash(b, buildArray().Hash) }

// BenchmarkHashArrayPrefixed hashes an Array with a non-empty prepend buffer.
func BenchmarkHashArrayPrefixed(b *testing.B) {
	a := buildArray()
	for j := 0; j < 16; j++ {
		a = a.Prepend(j)
	}
	benchHash(b, a.Hash)
}

func BenchmarkHashSet(b *testing.B) { benchHash(b, buildHashSet(benchKeys()).Hash) }

func BenchmarkHashList(b *testing.B) { benchHash(b, buildList().Hash) }

// BenchmarkKeySetArrayContainsSame looks up the very Array instances the set
// was built from.
func BenchmarkKeySetArrayContainsSame(b *testing.B) {
	keys := arrayKeys()
	s := HashSetFromSlice(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBool = s.Contains(keys[i%keyPool])
	}
}

// BenchmarkKeySetArrayContainsCopy looks up equal Arrays that are not the
// stored instances: Updated copies one root-to-leaf path, so each probe has
// its own root, hashed on its first lookup, and a hit ends in an element-wise
// Equal.
func BenchmarkKeySetArrayContainsCopy(b *testing.B) {
	keys := arrayKeys()
	s := HashSetFromSlice(keys)
	probes := make([]Array[int], keyPool)
	for i, k := range keys {
		probes[i] = k.Updated(0, k.Get(0))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBool = s.Contains(probes[i%keyPool])
	}
}

// BenchmarkKeySetArrayInsert builds a set of keyPool Array keys: splitting a
// HAMT leaf re-hashes the key already stored there.
func BenchmarkKeySetArrayInsert(b *testing.B) {
	keys := arrayKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = HashSetFromSlice(keys).Size()
	}
}

// benchMapSetContains looks up probes in a map keyed by setKeys().
func benchMapSetContains(b *testing.B, probes func(keys []HashSet[int]) []HashSet[int]) {
	keys := setKeys()
	m := EmptyHashMap[HashSet[int], int]()
	for i, k := range keys {
		m = m.Put(k, i)
	}
	ps := probes(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkBool = m.Contains(ps[i%keyPool])
	}
}

// BenchmarkKeyMapSetGetSame looks up the very HashSet instances the map was
// built from.
func BenchmarkKeyMapSetGetSame(b *testing.B) {
	benchMapSetContains(b, func(keys []HashSet[int]) []HashSet[int] { return keys })
}

// BenchmarkKeyMapSetGetFresh looks up HashSets built separately from the
// stored keys: equal, but not the same instances.
func BenchmarkKeyMapSetGetFresh(b *testing.B) {
	benchMapSetContains(b, func([]HashSet[int]) []HashSet[int] { return setKeys() })
}

// BenchmarkHashSetRemoveAdd removes and re-adds every element of a set.
func BenchmarkHashSetRemoveAdd(b *testing.B) {
	keys := benchKeys()
	s := buildHashSet(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys[:1000] {
			sinkInt = s.Remove(k).Add(k).Size()
		}
	}
}
