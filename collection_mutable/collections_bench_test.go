package collection_mutable

// Benchmarks of the mutable collections, reporting time and allocations per
// operation. Run with:
//
//	bazel run //collection_mutable:collections_bench -- -test.bench=. -test.benchmem -test.run='^$'

import "testing"

const benchSize = 10000

func benchKeys() []int {
	keys := make([]int, benchSize)
	for i := range keys {
		keys[i] = (i * 7919) % benchSize
	}
	return keys
}

func fillArray() *Array[int] {
	a := EmptyArray[int]()
	for j := 0; j < benchSize; j++ {
		a.Append(j)
	}
	return a
}

func fillHashMap(keys []int) *HashMap[int, int] {
	m := EmptyHashMap[int, int]()
	for _, k := range keys {
		m.Put(k, k)
	}
	return m
}

func fillHashSet(keys []int) *HashSet[int] {
	s := EmptyHashSet[int]()
	for _, k := range keys {
		s.Add(k)
	}
	return s
}

func fillTreeMap(keys []int) *TreeMap[int, int] {
	m := EmptyTreeMap[int, int]()
	for _, k := range keys {
		m.Put(k, k)
	}
	return m
}

func fillTreeSet(keys []int) *TreeSet[int] {
	s := EmptyTreeSet[int]()
	for _, k := range keys {
		s.Add(k)
	}
	return s
}

var sinkInt int
var sinkBool bool

func BenchmarkArrayAppend(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = fillArray().Size()
	}
}

func BenchmarkArrayDistinct(b *testing.B) {
	xs := make([]int, benchSize)
	for i := range xs {
		xs[i] = i % (benchSize / 2) // each value twice
	}
	a := ArrayFromSlice(xs)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = a.Distinct().Size()
	}
}

func BenchmarkArrayGetSet(b *testing.B) {
	a := fillArray()
	keys := benchKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			a.Set(k, a.Get(k)+1)
		}
	}
}

func BenchmarkListAppendPrepend(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := EmptyList[int]()
		for j := 0; j < benchSize/2; j++ {
			l.Append(j)
			l.Prepend(j)
		}
		sinkInt = l.Size()
	}
}

func BenchmarkListForEach(b *testing.B) {
	l := EmptyList[int]()
	for j := 0; j < benchSize; j++ {
		l.Append(j)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.ForEach(func(x int) { sinkInt += x })
	}
}

func BenchmarkListRemoveFirstLast(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := EmptyList[int]()
		for j := 0; j < 1000; j++ {
			l.Append(j)
		}
		for l.Size() > 1 {
			l.RemoveFirst()
			l.RemoveLast()
		}
		sinkInt = l.Size()
	}
}

func BenchmarkHashMapPut(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = fillHashMap(keys).Size()
	}
}

func BenchmarkHashMapGet(b *testing.B) {
	keys := benchKeys()
	m := fillHashMap(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = m.Get(k).IsDefined()
		}
	}
}

func BenchmarkHashMapPutRemove(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := fillHashMap(keys)
		for _, k := range keys {
			m.Remove(k)
		}
		sinkInt = m.Size()
	}
}

func BenchmarkHashSetAdd(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = fillHashSet(keys).Size()
	}
}

func BenchmarkHashSetContains(b *testing.B) {
	keys := benchKeys()
	s := fillHashSet(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = s.Contains(k)
		}
	}
}

func BenchmarkTreeMapPut(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = fillTreeMap(keys).Size()
	}
}

func BenchmarkTreeMapGet(b *testing.B) {
	keys := benchKeys()
	m := fillTreeMap(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = m.Get(k).IsDefined()
		}
	}
}

func BenchmarkTreeMapPutRemove(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := fillTreeMap(keys)
		for _, k := range keys {
			m.Remove(k)
		}
		sinkInt = m.Size()
	}
}

func BenchmarkTreeMapForEach(b *testing.B) {
	m := fillTreeMap(benchKeys())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ForEachKV(func(k int, v int) { sinkInt += v })
	}
}

func BenchmarkTreeSetAdd(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = fillTreeSet(keys).Size()
	}
}

func BenchmarkTreeSetContains(b *testing.B) {
	keys := benchKeys()
	s := fillTreeSet(keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			sinkBool = s.Contains(k)
		}
	}
}

func BenchmarkTreeSetAddRemove(b *testing.B) {
	keys := benchKeys()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := fillTreeSet(keys)
		for _, k := range keys {
			s.Remove(k)
		}
		sinkInt = s.Size()
	}
}
