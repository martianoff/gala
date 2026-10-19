package collection_immutable

// Benchmarks of the immutable collections, reporting time and allocations per
// operation. Run with:
//
//	bazel run //collection_immutable:collections_bench -- -test.bench=. -test.benchmem -test.run='^$'

import "testing"

const benchSize = 10000

func benchKeys() []int {
	keys := make([]int, benchSize)
	for i := range keys {
		keys[i] = (i * 7919) % benchSize
	}
	return keys
}

func buildList() List[int] {
	l := EmptyList[int]()
	for i := 0; i < benchSize; i++ {
		l = l.Prepend(i)
	}
	return l
}

func buildArray() Array[int] {
	a := EmptyArray[int]()
	for i := 0; i < benchSize; i++ {
		a = a.Append(i)
	}
	return a
}

func buildHashMap(keys []int) HashMap[int, int] {
	m := EmptyHashMap[int, int]()
	for _, k := range keys {
		m = m.Put(k, k)
	}
	return m
}

func buildHashSet(keys []int) HashSet[int] {
	s := EmptyHashSet[int]()
	for _, k := range keys {
		s = s.Add(k)
	}
	return s
}

func buildTreeMap(keys []int) TreeMap[int, int] {
	m := EmptyTreeMap[int, int]()
	for _, k := range keys {
		m = m.Put(k, k)
	}
	return m
}

func buildTreeSet(keys []int) TreeSet[int] {
	s := EmptyTreeSet[int]()
	for _, k := range keys {
		s = s.Add(k)
	}
	return s
}

var sinkInt int
var sinkBool bool

func BenchmarkListBuild(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = buildList().Size()
	}
}

func BenchmarkListMapFold(b *testing.B) {
	l := buildList()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = List_FoldLeft(List_Map(l, func(x int) int { return x + 1 }), 0, func(acc int, x int) int { return acc + x })
	}
}

func BenchmarkListFilter(b *testing.B) {
	l := buildList()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = l.Filter(func(x int) bool { return x%2 == 0 }).Size()
	}
}

func BenchmarkArrayBuild(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = buildArray().Size()
	}
}

func BenchmarkArrayGet(b *testing.B) {
	a := buildArray()
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkInt += a.Get(k)
		}
	}
}

func BenchmarkArrayUpdated(b *testing.B) {
	a := buildArray()
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := a
		for _, k := range keys[:1000] {
			x = x.Updated(k, k)
		}
		sinkInt = x.Size()
	}
}

func BenchmarkArrayForEach(b *testing.B) {
	a := buildArray()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.ForEach(func(x int) { sinkInt += x })
	}
}

func BenchmarkHashMapBuild(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = buildHashMap(keys).Size()
	}
}

func BenchmarkHashMapGet(b *testing.B) {
	keys := benchKeys()
	m := buildHashMap(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = m.Get(k).IsDefined()
		}
	}
}

func BenchmarkHashMapRemove(b *testing.B) {
	keys := benchKeys()
	m := buildHashMap(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := m
		for _, k := range keys[:1000] {
			x = x.Remove(k)
		}
		sinkInt = x.Size()
	}
}

func BenchmarkHashSetBuild(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = buildHashSet(keys).Size()
	}
}

func BenchmarkHashSetContains(b *testing.B) {
	keys := benchKeys()
	s := buildHashSet(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = s.Contains(k)
		}
	}
}

func BenchmarkTreeMapBuild(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = buildTreeMap(keys).Size()
	}
}

func BenchmarkTreeMapGet(b *testing.B) {
	keys := benchKeys()
	m := buildTreeMap(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = m.Get(k).IsDefined()
		}
	}
}

func BenchmarkTreeMapRemove(b *testing.B) {
	keys := benchKeys()
	m := buildTreeMap(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := m
		for _, k := range keys[:1000] {
			x = x.Remove(k)
		}
		sinkInt = x.Size()
	}
}

func BenchmarkTreeMapForEach(b *testing.B) {
	m := buildTreeMap(benchKeys())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ForEachKV(func(k int, v int) { sinkInt += v })
	}
}

func BenchmarkTreeSetBuild(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = buildTreeSet(keys).Size()
	}
}

func BenchmarkTreeSetContains(b *testing.B) {
	keys := benchKeys()
	s := buildTreeSet(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = s.Contains(k)
		}
	}
}

func BenchmarkTreeSetRemove(b *testing.B) {
	keys := benchKeys()
	s := buildTreeSet(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := s
		for _, k := range keys[:1000] {
			x = x.Remove(k)
		}
		sinkInt = x.Size()
	}
}
