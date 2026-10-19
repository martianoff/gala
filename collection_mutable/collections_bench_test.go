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

var sinkInt int
var sinkBool bool

func BenchmarkArrayAppend(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := EmptyArray[int]()
		for j := 0; j < benchSize; j++ {
			a.Append(j)
		}
		sinkInt = a.Size()
	}
}

func BenchmarkArrayGetSet(b *testing.B) {
	a := EmptyArray[int]()
	for j := 0; j < benchSize; j++ {
		a.Append(j)
	}
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
		m := EmptyHashMap[int, int]()
		for _, k := range keys {
			m.Put(k, k)
		}
		sinkInt = m.Size()
	}
}

func BenchmarkHashMapGet(b *testing.B) {
	keys := benchKeys()
	m := EmptyHashMap[int, int]()
	for _, k := range keys {
		m.Put(k, k)
	}
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
		m := EmptyHashMap[int, int]()
		for _, k := range keys {
			m.Put(k, k)
		}
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
		s := EmptyHashSet[int]()
		for _, k := range keys {
			s.Add(k)
		}
		sinkInt = s.Size()
	}
}

func BenchmarkHashSetContains(b *testing.B) {
	keys := benchKeys()
	s := EmptyHashSet[int]()
	for _, k := range keys {
		s.Add(k)
	}
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
		m := EmptyTreeMap[int, int]()
		for _, k := range keys {
			m.Put(k, k)
		}
		sinkInt = m.Size()
	}
}

func BenchmarkTreeMapGet(b *testing.B) {
	keys := benchKeys()
	m := EmptyTreeMap[int, int]()
	for _, k := range keys {
		m.Put(k, k)
	}
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
		m := EmptyTreeMap[int, int]()
		for _, k := range keys {
			m.Put(k, k)
		}
		for _, k := range keys {
			m.Remove(k)
		}
		sinkInt = m.Size()
	}
}

func BenchmarkTreeMapForEach(b *testing.B) {
	m := EmptyTreeMap[int, int]()
	for _, k := range benchKeys() {
		m.Put(k, k)
	}
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
		s := EmptyTreeSet[int]()
		for _, k := range keys {
			s.Add(k)
		}
		sinkInt = s.Size()
	}
}

func BenchmarkTreeSetContains(b *testing.B) {
	keys := benchKeys()
	s := EmptyTreeSet[int]()
	for _, k := range keys {
		s.Add(k)
	}
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
		s := EmptyTreeSet[int]()
		for _, k := range keys {
			s.Add(k)
		}
		for _, k := range keys {
			s.Remove(k)
		}
		sinkInt = s.Size()
	}
}
