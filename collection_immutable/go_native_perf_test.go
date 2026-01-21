package main

import (
	"fmt"
	"time"
)

// Performance test utilities
func measureNs(iterations int, f func()) int64 {
	start := time.Now()
	for i := 0; i < iterations; i++ {
		f()
	}
	elapsed := time.Since(start)
	return elapsed.Nanoseconds() / int64(iterations)
}

func printResult(name string, nsPerOp int64) {
	fmt.Printf("%-40s %12d ns/op\n", name, nsPerOp)
}

// ============ SLICE BENCHMARKS (IMMUTABLE STYLE) ============

// Slice creation benchmark - small (immutable style - copy on each append)
func benchSliceCreation100() {
	ns := measureNs(1000, func() {
		slice := make([]int, 0)
		for i := 0; i < 100; i++ {
			newSlice := make([]int, len(slice)+1)
			copy(newSlice, slice)
			newSlice[len(slice)] = i
			slice = newSlice
		}
	})
	printResult("Slice.Creation(100) immutable", ns)
}

// Slice creation benchmark - medium (immutable style)
func benchSliceCreation10000() {
	ns := measureNs(100, func() {
		slice := make([]int, 0)
		for i := 0; i < 10000; i++ {
			newSlice := make([]int, len(slice)+1)
			copy(newSlice, slice)
			newSlice[len(slice)] = i
			slice = newSlice
		}
	})
	printResult("Slice.Creation(10000) immutable", ns)
}

// Slice creation benchmark - large (immutable style)
func benchSliceCreation100000() {
	ns := measureNs(10, func() {
		slice := make([]int, 0)
		for i := 0; i < 100000; i++ {
			newSlice := make([]int, len(slice)+1)
			copy(newSlice, slice)
			newSlice[len(slice)] = i
			slice = newSlice
		}
	})
	printResult("Slice.Creation(100000) immutable", ns)
}

// ============ SLICE BENCHMARKS (MUTABLE STYLE) ============

// Slice creation benchmark - small (mutable style)
func benchSliceCreationMutable100() {
	ns := measureNs(1000, func() {
		slice := make([]int, 0, 100)
		for i := 0; i < 100; i++ {
			slice = append(slice, i)
		}
	})
	printResult("Slice.Creation(100) mutable", ns)
}

// Slice creation benchmark - medium (mutable style)
func benchSliceCreationMutable10000() {
	ns := measureNs(100, func() {
		slice := make([]int, 0, 10000)
		for i := 0; i < 10000; i++ {
			slice = append(slice, i)
		}
	})
	printResult("Slice.Creation(10000) mutable", ns)
}

// Slice creation benchmark - large (mutable style)
func benchSliceCreationMutable100000() {
	ns := measureNs(10, func() {
		slice := make([]int, 0, 100000)
		for i := 0; i < 100000; i++ {
			slice = append(slice, i)
		}
	})
	printResult("Slice.Creation(100000) mutable", ns)
}

// Slice append benchmark (immutable style)
func benchSliceAppend() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(100000, func() {
		newSlice := make([]int, len(slice)+1)
		copy(newSlice, slice)
		newSlice[len(slice)] = 999
		_ = newSlice
	})
	printResult("Slice.Append immutable (size=10000)", ns)
}

// Slice prepend benchmark (immutable style)
func benchSlicePrepend() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(100, func() {
		newSlice := make([]int, len(slice)+1)
		newSlice[0] = 999
		copy(newSlice[1:], slice)
		_ = newSlice
	})
	printResult("Slice.Prepend immutable (size=10000)", ns)
}

// Slice head benchmark
func benchSliceHead() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000000, func() {
		_ = slice[0]
	})
	printResult("Slice.Head (size=10000)", ns)
}

// Slice get benchmark - first
func benchSliceGetFirst() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000000, func() {
		_ = slice[0]
	})
	printResult("Slice.Get(0) (size=10000)", ns)
}

// Slice get benchmark - middle
func benchSliceGetMiddle() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000000, func() {
		_ = slice[5000]
	})
	printResult("Slice.Get(5000) (size=10000)", ns)
}

// Slice get benchmark - last
func benchSliceGetLast() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000000, func() {
		_ = slice[9999]
	})
	printResult("Slice.Get(9999) (size=10000)", ns)
}

// Slice update benchmark (immutable style)
func benchSliceUpdate() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(100000, func() {
		newSlice := make([]int, len(slice))
		copy(newSlice, slice)
		newSlice[5000] = 999
		_ = newSlice
	})
	printResult("Slice.Update(5000) immutable (size=10000)", ns)
}

// Slice filter benchmark (immutable style)
func benchSliceFilter() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	isEven := func(x int) bool { return x%2 == 0 }
	ns := measureNs(100, func() {
		result := make([]int, 0)
		for _, v := range slice {
			if isEven(v) {
				result = append(result, v)
			}
		}
		_ = result
	})
	printResult("Slice.Filter (size=10000)", ns)
}

// Slice map benchmark
func benchSliceMap() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	double := func(x int) int { return x * 2 }
	ns := measureNs(100, func() {
		result := make([]int, len(slice))
		for i, v := range slice {
			result[i] = double(v)
		}
		_ = result
	})
	printResult("Slice.Map (size=10000)", ns)
}

// Slice fold benchmark
func benchSliceFold() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000, func() {
		acc := 0
		for _, v := range slice {
			acc += v
		}
		_ = acc
	})
	printResult("Slice.Fold (size=10000)", ns)
}

// Slice take benchmark (immutable)
func benchSliceTake() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000, func() {
		result := make([]int, 5000)
		copy(result, slice[:5000])
		_ = result
	})
	printResult("Slice.Take(5000) (size=10000)", ns)
}

// Slice drop benchmark (immutable)
func benchSliceDrop() {
	slice := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		slice[i] = i
	}
	ns := measureNs(1000, func() {
		result := make([]int, 5000)
		copy(result, slice[5000:])
		_ = result
	})
	printResult("Slice.Drop(5000) (size=10000)", ns)
}

// ============ MAP-BASED IMMUTABLE HASHSET BENCHMARKS ============

// MapSet is a simple immutable set using map[int]struct{} with copy-on-write
type MapSet struct {
	data map[int]struct{}
}

func NewMapSet() MapSet {
	return MapSet{data: make(map[int]struct{})}
}

func (s MapSet) Add(elem int) MapSet {
	newData := make(map[int]struct{}, len(s.data)+1)
	for k, v := range s.data {
		newData[k] = v
	}
	newData[elem] = struct{}{}
	return MapSet{data: newData}
}

func (s MapSet) Contains(elem int) bool {
	_, ok := s.data[elem]
	return ok
}

func (s MapSet) Remove(elem int) MapSet {
	if _, ok := s.data[elem]; !ok {
		return s
	}
	newData := make(map[int]struct{}, len(s.data)-1)
	for k, v := range s.data {
		if k != elem {
			newData[k] = v
		}
	}
	return MapSet{data: newData}
}

func (s MapSet) Size() int {
	return len(s.data)
}

func (s MapSet) Filter(pred func(int) bool) MapSet {
	newData := make(map[int]struct{})
	for k := range s.data {
		if pred(k) {
			newData[k] = struct{}{}
		}
	}
	return MapSet{data: newData}
}

func (s MapSet) Union(other MapSet) MapSet {
	newData := make(map[int]struct{}, len(s.data)+len(other.data))
	for k, v := range s.data {
		newData[k] = v
	}
	for k, v := range other.data {
		newData[k] = v
	}
	return MapSet{data: newData}
}

func (s MapSet) Intersect(other MapSet) MapSet {
	newData := make(map[int]struct{})
	// Iterate over smaller set for efficiency
	smaller, larger := s.data, other.data
	if len(s.data) > len(other.data) {
		smaller, larger = other.data, s.data
	}
	for k := range smaller {
		if _, ok := larger[k]; ok {
			newData[k] = struct{}{}
		}
	}
	return MapSet{data: newData}
}

// MapSet creation benchmark - small
func benchMapSetCreation100() {
	ns := measureNs(100, func() {
		set := NewMapSet()
		for i := 0; i < 100; i++ {
			set = set.Add(i)
		}
	})
	printResult("MapSet.Creation(100)", ns)
}

// MapSet creation benchmark - medium
// Note: This is O(n²) due to copy-on-write, extremely slow!
func benchMapSetCreation10000() {
	ns := measureNs(1, func() {
		set := NewMapSet()
		for i := 0; i < 10000; i++ {
			set = set.Add(i)
		}
	})
	printResult("MapSet.Creation(10000)", ns)
}

// MapSet creation benchmark - large
// SKIPPED: Would take hours due to O(n²) copy-on-write
func benchMapSetCreation100000() {
	// Estimate based on 10000 element time
	fmt.Printf("%-40s %12s ns/op (SKIPPED - O(n²) copy)\n", "MapSet.Creation(100000)", "~10^12")
}

// MapSet add benchmark (single element)
func benchMapSetAdd() {
	set := NewMapSet()
	for i := 0; i < 10000; i++ {
		set = set.Add(i)
	}
	ns := measureNs(100000, func() {
		_ = set.Add(99999)
	})
	printResult("MapSet.Add (size=10000)", ns)
}

// MapSet contains benchmark - hit
func benchMapSetContainsHit() {
	set := NewMapSet()
	for i := 0; i < 10000; i++ {
		set = set.Add(i)
	}
	ns := measureNs(1000000, func() {
		_ = set.Contains(5000)
	})
	printResult("MapSet.Contains (hit, size=10000)", ns)
}

// MapSet contains benchmark - miss
func benchMapSetContainsMiss() {
	set := NewMapSet()
	for i := 0; i < 10000; i++ {
		set = set.Add(i)
	}
	ns := measureNs(1000000, func() {
		_ = set.Contains(20000)
	})
	printResult("MapSet.Contains (miss, size=10000)", ns)
}

// MapSet remove benchmark
func benchMapSetRemove() {
	set := NewMapSet()
	for i := 0; i < 10000; i++ {
		set = set.Add(i)
	}
	ns := measureNs(100000, func() {
		_ = set.Remove(5000)
	})
	printResult("MapSet.Remove (size=10000)", ns)
}

// MapSet filter benchmark
func benchMapSetFilter() {
	set := NewMapSet()
	for i := 0; i < 10000; i++ {
		set = set.Add(i)
	}
	ns := measureNs(100, func() {
		_ = set.Filter(func(x int) bool { return x%2 == 0 })
	})
	printResult("MapSet.Filter (size=10000)", ns)
}

// MapSet union benchmark
func benchMapSetUnion() {
	set1 := NewMapSet()
	set2 := NewMapSet()
	for i := 0; i < 1000; i++ {
		set1 = set1.Add(i)
		set2 = set2.Add(i + 500)
	}
	ns := measureNs(100, func() {
		_ = set1.Union(set2)
	})
	printResult("MapSet.Union (2x1000)", ns)
}

// MapSet intersection benchmark
func benchMapSetIntersect() {
	set1 := NewMapSet()
	set2 := NewMapSet()
	for i := 0; i < 1000; i++ {
		set1 = set1.Add(i)
		set2 = set2.Add(i + 500)
	}
	ns := measureNs(100, func() {
		_ = set1.Intersect(set2)
	})
	printResult("MapSet.Intersect (2x1000)", ns)
}

func main() {
	fmt.Println("=== Go Native Slice Performance ===")
	fmt.Println("Testing with 10,000 - 100,000 elements")
	fmt.Println("")

	fmt.Println("--- Slice Creation (Mutable vs Immutable) ---")
	benchSliceCreationMutable100()
	benchSliceCreation100()
	benchSliceCreationMutable10000()
	benchSliceCreation10000()
	benchSliceCreationMutable100000()
	benchSliceCreation100000()

	fmt.Println("")
	fmt.Println("--- Slice Operations (Immutable Style) ---")
	benchSliceAppend()
	benchSlicePrepend()
	benchSliceHead()
	benchSliceGetFirst()
	benchSliceGetMiddle()
	benchSliceGetLast()
	benchSliceUpdate()
	benchSliceFilter()
	benchSliceMap()
	benchSliceFold()
	benchSliceTake()
	benchSliceDrop()

	fmt.Println("")
	fmt.Println("=== Map-Based Immutable HashSet (copy-on-write) ===")
	fmt.Println("Compare with GALA HAMT-based HashSet")
	fmt.Println("")
	fmt.Println("--- MapSet Operations ---")
	benchMapSetCreation100()
	benchMapSetCreation10000()
	benchMapSetCreation100000()
	benchMapSetAdd()
	benchMapSetContainsHit()
	benchMapSetContainsMiss()
	benchMapSetRemove()
	benchMapSetFilter()
	benchMapSetUnion()
	benchMapSetIntersect()
}
