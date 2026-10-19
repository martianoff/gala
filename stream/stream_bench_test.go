package stream

// Benchmarks of Stream, reporting time and allocations per operation. Run with:
//
//	bazel run //stream:stream_bench -- -test.bench=. -test.benchmem -test.run='^$'

import "testing"

var sinkInt int

func BenchmarkStreamRangeFold(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = Stream_Fold(Range(0, 10000), 0, func(acc int, x int) int { return acc + x })
	}
}

func BenchmarkStreamMapFilterTake(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := Stream_Map(From(0), func(x int) int { return x * 3 }).Filter(func(x int) bool { return x%2 == 0 }).Take(500)
		sinkInt = s.ToList().Size()
	}
}

func BenchmarkStreamForEach(b *testing.B) {
	s := Range(0, 10000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.ForEach(func(x int) { sinkInt += x })
	}
}
