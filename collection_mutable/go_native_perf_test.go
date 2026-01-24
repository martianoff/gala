package main

import (
	"container/list"
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
	fmt.Printf("%-45s %12d ns/op\n", name, nsPerOp)
}

// ============ GO SLICE BENCHMARKS ============

// Slice creation benchmark - small
func benchSliceCreation100() {
	ns := measureNs(10000, func() {
		s := make([]int, 0)
		for i := 0; i < 100; i++ {
			s = append(s, i)
		}
	})
	printResult("GoSlice.Creation(100)", ns)
}

// Slice creation benchmark - medium
func benchSliceCreation10000() {
	ns := measureNs(1000, func() {
		s := make([]int, 0)
		for i := 0; i < 10000; i++ {
			s = append(s, i)
		}
	})
	printResult("GoSlice.Creation(10000)", ns)
}

// Slice creation benchmark - large
func benchSliceCreation100000() {
	ns := measureNs(100, func() {
		s := make([]int, 0)
		for i := 0; i < 100000; i++ {
			s = append(s, i)
		}
	})
	printResult("GoSlice.Creation(100000)", ns)
}

// Slice creation with capacity - medium
func benchSliceCreationWithCapacity10000() {
	ns := measureNs(1000, func() {
		s := make([]int, 0, 10000)
		for i := 0; i < 10000; i++ {
			s = append(s, i)
		}
	})
	printResult("GoSlice.CreationWithCap(10000)", ns)
}

// Slice append benchmark
func benchSliceAppend() {
	s := make([]int, 0, 10001)
	for i := 0; i < 10000; i++ {
		s = append(s, i)
	}
	ns := measureNs(1000000, func() {
		s = append(s, 999)
		s = s[:len(s)-1]
	})
	printResult("GoSlice.Append (size=10000)", ns)
}

// Slice prepend benchmark (inefficient O(n))
func benchSlicePrepend() {
	s := make([]int, 0, 10001)
	for i := 0; i < 10000; i++ {
		s = append(s, i)
	}
	ns := measureNs(1000, func() {
		s = append([]int{999}, s...)
		s = s[1:]
	})
	printResult("GoSlice.Prepend (size=10000)", ns)
}

// Slice access benchmark - beginning
func benchSliceGetFirst() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(10000000, func() {
		_ = s[0]
	})
	printResult("GoSlice.Get(0) (size=10000)", ns)
}

// Slice access benchmark - middle
func benchSliceGetMiddle() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(10000000, func() {
		_ = s[5000]
	})
	printResult("GoSlice.Get(5000) (size=10000)", ns)
}

// Slice access benchmark - end
func benchSliceGetLast() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(10000000, func() {
		_ = s[9999]
	})
	printResult("GoSlice.Get(9999) (size=10000)", ns)
}

// Slice set benchmark
func benchSliceSet() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(10000000, func() {
		s[5000] = 999
	})
	printResult("GoSlice.Set(5000) (size=10000)", ns)
}

// Slice filter benchmark (manual implementation)
func benchSliceFilter() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(1000, func() {
		result := make([]int, 0)
		for _, v := range s {
			if v%2 == 0 {
				result = append(result, v)
			}
		}
	})
	printResult("GoSlice.Filter (size=10000)", ns)
}

// Slice map benchmark (manual implementation)
func benchSliceMap() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(1000, func() {
		result := make([]int, len(s))
		for i, v := range s {
			result[i] = v * 2
		}
	})
	printResult("GoSlice.Map (size=10000)", ns)
}

// Slice fold benchmark (manual implementation)
func benchSliceFold() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(10000, func() {
		acc := 0
		for _, v := range s {
			acc += v
		}
	})
	printResult("GoSlice.FoldLeft (size=10000)", ns)
}

// Slice copy benchmark
func benchSliceCopy() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(1000, func() {
		result := make([]int, len(s))
		copy(result, s)
	})
	printResult("GoSlice.Copy (size=10000)", ns)
}

// Slice reverse benchmark
func benchSliceReverse() {
	s := make([]int, 10000)
	for i := 0; i < 10000; i++ {
		s[i] = i
	}
	ns := measureNs(10000, func() {
		for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
			s[i], s[j] = s[j], s[i]
		}
	})
	printResult("GoSlice.Reverse (size=10000)", ns)
}

// ============ GO container/list BENCHMARKS ============

// container/list creation benchmark - small
func benchListCreation100() {
	ns := measureNs(10000, func() {
		l := list.New()
		for i := 0; i < 100; i++ {
			l.PushBack(i)
		}
	})
	printResult("GoList.Creation(100)", ns)
}

// container/list creation benchmark - medium
func benchListCreation10000() {
	ns := measureNs(1000, func() {
		l := list.New()
		for i := 0; i < 10000; i++ {
			l.PushBack(i)
		}
	})
	printResult("GoList.Creation(10000)", ns)
}

// container/list creation benchmark - large
func benchListCreation100000() {
	ns := measureNs(100, func() {
		l := list.New()
		for i := 0; i < 100000; i++ {
			l.PushBack(i)
		}
	})
	printResult("GoList.Creation(100000)", ns)
}

// container/list prepend benchmark (O(1))
func benchListPrepend() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000000, func() {
		e := l.PushFront(999)
		l.Remove(e)
	})
	printResult("GoList.Prepend (size=10000)", ns)
}

// container/list append benchmark (O(1))
func benchListAppend() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000000, func() {
		e := l.PushBack(999)
		l.Remove(e)
	})
	printResult("GoList.Append (size=10000)", ns)
}

// container/list head benchmark (O(1))
func benchListHead() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(10000000, func() {
		_ = l.Front().Value
	})
	printResult("GoList.Head (size=10000)", ns)
}

// container/list last benchmark (O(1))
func benchListLast() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(10000000, func() {
		_ = l.Back().Value
	})
	printResult("GoList.Last (size=10000)", ns)
}

// container/list get benchmark - middle (O(n))
func benchListGetMiddle() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000, func() {
		e := l.Front()
		for i := 0; i < 5000; i++ {
			e = e.Next()
		}
		_ = e.Value
	})
	printResult("GoList.Get(5000) (size=10000)", ns)
}

// container/list filter benchmark (manual implementation)
func benchListFilter() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000, func() {
		result := list.New()
		for e := l.Front(); e != nil; e = e.Next() {
			if e.Value.(int)%2 == 0 {
				result.PushBack(e.Value)
			}
		}
	})
	printResult("GoList.Filter (size=10000)", ns)
}

// container/list map benchmark (manual implementation)
func benchListMap() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000, func() {
		result := list.New()
		for e := l.Front(); e != nil; e = e.Next() {
			result.PushBack(e.Value.(int) * 2)
		}
	})
	printResult("GoList.Map (size=10000)", ns)
}

// container/list fold benchmark (manual implementation)
func benchListFold() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(10000, func() {
		acc := 0
		for e := l.Front(); e != nil; e = e.Next() {
			acc += e.Value.(int)
		}
	})
	printResult("GoList.FoldLeft (size=10000)", ns)
}

// container/list removeFirst benchmark
func benchListRemoveFirst() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000000, func() {
		l.PushFront(0)
		l.Remove(l.Front())
	})
	printResult("GoList.RemoveFirst (size=10000)", ns)
}

// container/list removeLast benchmark
func benchListRemoveLast() {
	l := list.New()
	for i := 0; i < 10000; i++ {
		l.PushBack(i)
	}
	ns := measureNs(1000000, func() {
		l.PushBack(0)
		l.Remove(l.Back())
	})
	printResult("GoList.RemoveLast (size=10000)", ns)
}

func main() {
	fmt.Println("=== Go Native Collections Performance ===")
	fmt.Println("Testing with 100 - 100,000 elements")
	fmt.Println("")

	fmt.Println("--- Go Slice Operations ---")
	benchSliceCreation100()
	benchSliceCreation10000()
	benchSliceCreation100000()
	benchSliceCreationWithCapacity10000()
	benchSliceAppend()
	benchSlicePrepend()
	benchSliceGetFirst()
	benchSliceGetMiddle()
	benchSliceGetLast()
	benchSliceSet()
	benchSliceFilter()
	benchSliceMap()
	benchSliceFold()
	benchSliceCopy()
	benchSliceReverse()

	fmt.Println("")
	fmt.Println("--- Go container/list Operations ---")
	benchListCreation100()
	benchListCreation10000()
	benchListCreation100000()
	benchListPrepend()
	benchListAppend()
	benchListHead()
	benchListLast()
	benchListGetMiddle()
	benchListFilter()
	benchListMap()
	benchListFold()
	benchListRemoveFirst()
	benchListRemoveLast()
}
