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
	fmt.Printf("%-30s %10d ns/op\n", name, nsPerOp)
}

// Slice creation benchmark (immutable style - copy on each append)
func benchSliceCreation() {
	ns := measureNs(10000, func() {
		slice := make([]int, 0)
		for i := 0; i < 30; i++ {
			newSlice := make([]int, len(slice)+1)
			copy(newSlice, slice)
			newSlice[len(slice)] = i
			slice = newSlice
		}
	})
	printResult("SliceCreation(30) immutable", ns)
}

// Slice creation benchmark (mutable style)
func benchSliceCreationMutable() {
	ns := measureNs(10000, func() {
		slice := make([]int, 0, 30)
		for i := 0; i < 30; i++ {
			slice = append(slice, i)
		}
	})
	printResult("SliceCreation(30) mutable", ns)
}

// Slice append benchmark (immutable style)
func benchSliceAppend() {
	slice := make([]int, 30)
	for i := 0; i < 30; i++ {
		slice[i] = i
	}
	ns := measureNs(100000, func() {
		newSlice := make([]int, len(slice)+1)
		copy(newSlice, slice)
		newSlice[len(slice)] = 999
		_ = newSlice
	})
	printResult("SliceAppend immutable", ns)
}

// Slice head benchmark
func benchSliceHead() {
	slice := make([]int, 30)
	for i := 0; i < 30; i++ {
		slice[i] = i
	}
	ns := measureNs(1000000, func() {
		_ = slice[0]
	})
	printResult("SliceHead", ns)
}

// Slice get benchmark
func benchSliceGet() {
	slice := make([]int, 30)
	for i := 0; i < 30; i++ {
		slice[i] = i
	}
	ns := measureNs(1000000, func() {
		_ = slice[15]
	})
	printResult("SliceGet(15)", ns)
}

// Slice filter benchmark (immutable style)
func benchSliceFilter() {
	slice := make([]int, 30)
	for i := 0; i < 30; i++ {
		slice[i] = i
	}
	isEven := func(x int) bool { return x%2 == 0 }
	ns := measureNs(10000, func() {
		result := make([]int, 0)
		for _, v := range slice {
			if isEven(v) {
				result = append(result, v)
			}
		}
		_ = result
	})
	printResult("SliceFilter", ns)
}

func main() {
	fmt.Println("=== Go Native Slice Performance ===")
	fmt.Println("")
	fmt.Println("--- Slice Operations ---")
	benchSliceCreationMutable()
	benchSliceCreation()
	benchSliceAppend()
	benchSliceHead()
	benchSliceGet()
	benchSliceFilter()
}
