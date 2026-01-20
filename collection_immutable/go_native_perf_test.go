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
}
