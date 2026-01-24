# Mutable Collections

This document describes the mutable collection data structures available in GALA's `collection_mutable` package.

## Overview

The `collection_mutable` package provides mutable data structures optimized for in-place modifications. These collections are ideal when you need to build up data incrementally or when immutability overhead is unacceptable.

### Import

```gala
import . "martianoff/gala/collection_mutable"
```

## Performance Characteristics

| Operation | Array | List | Notes |
|-----------|-------|------|-------|
| Get/Set | O(1) | O(n) | Array: direct index; List: traversal |
| Head/Last | O(1) | O(1) | |
| Append | O(1)* | O(1) | Array uses Go slice growth |
| AppendAll | O(m)* | O(m) | m = elements added |
| Prepend | O(n) | O(1) | Array shifts all elements |
| PrependAll | O(n+m) | O(m) | |
| Insert | O(n) | O(n) | |
| RemoveAt | O(n) | O(n) | Array shifts; List traverses |
| RemoveFirst | O(1) | O(1) | Array: slice view |
| RemoveLast | O(1) | O(1) | Array: slice view; List: O(1) with tail ptr |
| Length | O(1) | O(1) | |

**Legend:**
- O(1) - Constant time
- O(1)* - Amortized constant time (may occasionally reallocate)
- O(n) - Linear time in current size
- O(m) - Linear time in elements being added

---

## Array[T]

A mutable indexed sequence backed by a Go slice. Best for random access and append operations.

### Construction

```gala
// Empty array
var arr = EmptyArray[int]()

// From elements (pre-allocates capacity)
var arr = ArrayOf[int](1, 2, 3, 4, 5)

// From slice (copies the slice)
var slice = []int{1, 2, 3}
var arr = ArrayFromSlice[int](slice)

// With pre-allocated capacity (for known sizes)
var arr = ArrayWithCapacity[int](1000)
```

### Basic Operations

```gala
var arr = ArrayOf[int](1, 2, 3, 4, 5)

arr.IsEmpty()      // false
arr.NonEmpty()     // true
arr.Length()       // 5
arr.Size()         // 5 (alias for Length)
arr.Capacity()     // >= 5
```

### Element Access - O(1)

```gala
var arr = ArrayOf[int](10, 20, 30)

// Direct access
arr.Get(0)               // 10
arr.Get(1)               // 20
arr.GetOption(1)         // Some(20)
arr.GetOption(10)        // None

// Head/Last
arr.Head()               // 10
arr.HeadOption()         // Some(10)
arr.Last()               // 30
arr.LastOption()         // Some(30)
```

### Mutation Operations

```gala
var arr = ArrayOf[int](1, 2, 3)

// Set element - O(1)
arr.Set(1, 99)           // arr is now [1, 99, 3]

// Append - O(1) amortized
arr.Append(4)            // arr is now [1, 99, 3, 4]

// Prepend - O(n)
arr.Prepend(0)           // arr is now [0, 1, 99, 3, 4]

// Insert at index - O(n)
arr.Insert(2, 50)        // arr is now [0, 1, 50, 99, 3, 4]

// Remove at index - O(n)
arr.RemoveAt(2)          // arr is now [0, 1, 99, 3, 4]

// Remove first/last - O(1) (slice view operations)
arr.RemoveFirst()        // returns 0, arr is now [1, 99, 3, 4]
arr.RemoveLast()         // returns 4, arr is now [1, 99, 3]

// Clear
arr.Clear()              // arr is now []

// Reverse in-place
arr.Reverse()
```

### Bulk Operations

```gala
var arr = ArrayOf[int](1, 2, 3)

// Append from a slice - O(m) amortized
var more = []int{4, 5, 6}
arr.AppendAll(more)

// Append from another Array - O(m) amortized
var other = ArrayOf[int](7, 8, 9)
arr.AppendFrom(other)

// Prepend from a slice - O(n+m)
arr.PrependAll([]int{-2, -1, 0})
```

### Structural Operations (return new arrays)

```gala
var arr = ArrayOf[int](1, 2, 3, 4, 5)

// Take first n elements
arr.Take(3)              // new Array(1, 2, 3)

// Drop first n elements
arr.Drop(2)              // new Array(3, 4, 5)

// Slice [start, end)
arr.Slice(1, 4)          // new Array(2, 3, 4)

// Clone (shallow copy)
arr.Clone()              // new Array(1, 2, 3, 4, 5)

// Tail (all except first)
arr.Tail()               // new Array(2, 3, 4, 5)

// Init (all except last)
arr.Init()               // new Array(1, 2, 3, 4)

// Reversed copy
arr.Reversed()           // new Array(5, 4, 3, 2, 1)
```

### Functional Operations

```gala
var arr = ArrayOf[int](1, 2, 3, 4, 5)

// Map - returns new Array
var doubled = arr.Map[int]((x int) => x * 2)  // Array(2, 4, 6, 8, 10)

// Filter - returns new Array
var evens = arr.Filter((x int) => x % 2 == 0)  // Array(2, 4)

// FoldLeft
var sum = arr.FoldLeft[int](0, (acc int, x int) => acc + x)  // 15

// FoldRight
var result = arr.FoldRight[string]("", (x int, acc string) => {
    return fmt.Sprintf("%d%s", x, acc)
})  // "12345"
```

### Searching

```gala
var arr = ArrayOf[int](1, 2, 3, 2, 1)

arr.Contains(3)              // true
arr.IndexOf(2)               // 1
arr.LastIndexOf(2)           // 3
arr.Find((x int) => x > 2)   // Some(3)
arr.Exists((x int) => x > 4) // true
arr.ForAll((x int) => x > 0) // true
arr.Count((x int) => x == 2) // 2
```

### Grouping

```gala
var arr = ArrayOf[int](1, 2, 3, 4, 5)

// Split into groups of size n
arr.Grouped(2)
// Array(Array(1, 2), Array(3, 4), Array(5))

// Group by key function
arr.GroupBy[string]((x int) => {
    if x % 2 == 0 { return "even" }
    return "odd"
})
// map["even": Array(2, 4), "odd": Array(1, 3, 5)]
```

### Distinct

```gala
var arr = ArrayOf[int](1, 2, 2, 3, 3, 3)
arr.Distinct()  // Array(1, 2, 3)
```

### Conversion

```gala
var arr = ArrayOf[int](1, 2, 3)

arr.ToSlice()    // []int{1, 2, 3}
arr.String()     // "Array(1, 2, 3)"
```

### ForEach

```gala
arr.ForEach((x int) => {
    fmt.Println(x)
})
```

---

## List[T]

A mutable doubly-linked list. Best for frequent insertions/removals at both ends.

### Construction

```gala
// Empty list
var list = EmptyList[int]()

// From elements
var list = ListOf[int](1, 2, 3, 4, 5)

// From slice
var slice = []int{1, 2, 3}
var list = ListFromSlice[int](slice)
```

### Basic Operations

```gala
var list = ListOf[int](1, 2, 3, 4, 5)

list.IsEmpty()     // false
list.NonEmpty()    // true
list.Length()      // 5
list.Size()        // 5
```

### Element Access

```gala
var list = ListOf[int](10, 20, 30)

// Head/Last - O(1)
list.Head()              // 10
list.HeadOption()        // Some(10)
list.Last()              // 30
list.LastOption()        // Some(30)

// Index access - O(n)
list.Get(1)              // 20
list.GetOption(1)        // Some(20)
```

### Mutation Operations - O(1) at ends

```gala
var list = ListOf[int](2, 3, 4)

// Prepend - O(1)
list.Prepend(1)          // list is now [1, 2, 3, 4]

// Append - O(1)
list.Append(5)           // list is now [1, 2, 3, 4, 5]

// Remove first - O(1)
list.RemoveFirst()       // returns 1, list is now [2, 3, 4, 5]

// Remove last - O(1)
list.RemoveLast()        // returns 5, list is now [2, 3, 4]

// Clear
list.Clear()

// Reverse in-place
list.Reverse()
```

### Bulk Operations

```gala
var list = ListOf[int](1, 2, 3)

// Prepend multiple
list.PrependAll(ListOf[int](-1, 0))  // [-1, 0, 1, 2, 3]

// Append multiple
list.AppendAll(ListOf[int](4, 5))    // [-1, 0, 1, 2, 3, 4, 5]
```

### Functional Operations

```gala
var list = ListOf[int](1, 2, 3, 4, 5)

// Map
var doubled = list.Map[int]((x int) => x * 2)

// Filter
var evens = list.Filter((x int) => x % 2 == 0)

// FoldLeft
var sum = list.FoldLeft[int](0, (acc int, x int) => acc + x)

// FoldRight
var result = list.FoldRight[string]("", foldHelper)
```

### Searching

```gala
var list = ListOf[int](1, 2, 3, 2, 1)

list.Contains(3)              // true
list.IndexOf(2)               // 1
list.LastIndexOf(2)           // 3
list.Find((x int) => x > 2)   // Some(3)
list.Exists((x int) => x > 4) // true
list.ForAll((x int) => x > 0) // true
list.Count((x int) => x == 2) // 2
```

### Conversion

```gala
var list = ListOf[int](1, 2, 3)

list.ToSlice()    // []int{1, 2, 3}
list.String()     // "List(1, 2, 3)"
```

---

## Pattern Matching with Mutable Collections

GALA supports pattern matching on mutable collections. Since Array and List are generic structs, you can use type matching.

### Basic Type Matching

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
)

func describeArray[T any](arr *Array[T]) string {
    return arr match {
        case a: *Array[_] if a.IsEmpty() => "empty array"
        case a: *Array[_] if a.Length() == 1 => fmt.Sprintf("single element: %v", a.Head())
        case a: *Array[_] => fmt.Sprintf("array with %d elements", a.Length())
        case _ => "unknown"
    }
}

func main() {
    var empty = EmptyArray[int]()
    var single = ArrayOf[int](42)
    var multi = ArrayOf[int](1, 2, 3)

    fmt.Println(describeArray[int](empty))   // "empty array"
    fmt.Println(describeArray[int](single))  // "single element: 42"
    fmt.Println(describeArray[int](multi))   // "array with 3 elements"
}
```

### Processing with Guards

```gala
func processNumbers(arr *Array[int]) string {
    return arr match {
        case a: *Array[int] if a.IsEmpty() => "no numbers"
        case a: *Array[int] if a.ForAll((x int) => x > 0) => "all positive"
        case a: *Array[int] if a.ForAll((x int) => x < 0) => "all negative"
        case a: *Array[int] if a.Exists((x int) => x == 0) => "contains zero"
        case _ => "mixed numbers"
    }
}
```

### Option Pattern Matching with Collections

```gala
func findFirstEven(arr *Array[int]) string {
    var found = arr.Find((x int) => x % 2 == 0)
    return found match {
        case Some(value) => fmt.Sprintf("found even: %d", value)
        case None() => "no even numbers"
    }
}

func getMiddleElement[T any](arr *Array[T]) string {
    if arr.Length() < 3 {
        return "array too short"
    }
    var mid = arr.Length() / 2
    var opt = arr.GetOption(mid)
    return opt match {
        case Some(v) => fmt.Sprintf("middle: %v", v)
        case None() => "no middle"
    }
}
```

### List Processing with Pattern Matching

```gala
func sumList(list *List[int]) int {
    if list.IsEmpty() {
        return 0
    }
    return list.Head() + sumList(list.Tail())
}

func describeList[T any](list *List[T]) string {
    return list match {
        case l: *List[_] if l.IsEmpty() => "empty list"
        case l: *List[_] if l.Length() == 1 => "singleton"
        case l: *List[_] => fmt.Sprintf("list[%d]", l.Length())
    }
}
```

### Combining with Tuples

```gala
func analyzeArrays(a1 *Array[int], a2 *Array[int]) string {
    var tuple = (a1.Length(), a2.Length())
    return tuple match {
        case (0, 0) => "both empty"
        case (0, _) => "first empty"
        case (_, 0) => "second empty"
        case (n1, n2) if n1 == n2 => fmt.Sprintf("same size: %d", n1)
        case (n1, n2) if n1 > n2 => "first is larger"
        case _ => "second is larger"
    }
}
```

---

## Choosing Between Array and List

| Use Case | Recommended |
|----------|-------------|
| Random access by index | Array |
| Frequent appends | Array |
| Frequent prepends | List |
| Queue (FIFO) operations | List |
| Stack (LIFO) operations | Array or List |
| Building incrementally from end | Array |
| Building incrementally from front | List |
| Large collections with updates | Array |

### Array Advantages
- O(1) random access
- O(1) amortized append
- Better cache locality
- Lower memory overhead per element

### List Advantages
- O(1) prepend and append
- O(1) remove from both ends
- No reallocation on growth
- Stable references during iteration

---

## Performance Benchmarks

Benchmark results comparing GALA mutable collections to Go native equivalents.

### Running the Benchmarks

```shell
# GALA mutable collections benchmark
bazel run //collection_mutable:perf_gala

# Go native benchmark
bazel run //collection_mutable:perf_go
```

### Array vs Go Slice (ns/op) - 10,000 Elements

| Operation | GALA Array | Go Slice | Notes |
|-----------|----------:|----------:|-------|
| Creation | 34,577 | 29,551 | ~same |
| CreationWithCap | 11,407 | 8,274 | ~same |
| Append | 2 | 1 | Both O(1) amortized |
| Prepend | 1,021 | 6,218 | **GALA 6x faster** |
| Get(5000) | 1 | 0 | Both O(1) |
| Set(5000) | 1 | 0 | Both O(1) |
| Filter | 15,958 | 14,363 | ~same |
| Map | 9,505 | 8,109 | ~same |
| FoldLeft | 851 | 907 | ~same |
| Clone | 9,185 | 6,188 | ~same |
| Reverse | 2,907 | 1,929 | ~same |

### List vs Go container/list (ns/op) - 10,000 Elements

| Operation | GALA List | Go List | Notes |
|-----------|----------:|--------:|-------|
| Creation | 119,868 | 230,021 | **GALA 48% faster** |
| Prepend | 13 | 19 | **GALA 32% faster** |
| Append | 14 | 20 | **GALA 30% faster** |
| Head | 0 | 1 | Both O(1) |
| Last | 1 | 1 | Both O(1) |
| Get(5000) | 3,506 | 4,356 | **GALA 20% faster** |
| Filter | 74,552 | 100,465 | **GALA 26% faster** |
| Map | 133,013 | 294,430 | **GALA 2.2x faster** |
| FoldLeft | 9,221 | 9,450 | ~same |
| RemoveFirst | 14 | 20 | **GALA 30% faster** |
| RemoveLast | 15 | 20 | **GALA 25% faster** |

### Scaling Results

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| Array.Creation | 477 ns | 34,577 ns | 445,573 ns |
| List.Creation | 1,253 ns | 119,868 ns | 1,491,178 ns |
| GoSlice.Creation | 472 ns | 29,551 ns | 384,056 ns |
| GoList.Creation | 2,061 ns | 230,021 ns | 3,769,234 ns |

### Key Performance Insights

**Array Strengths:**
- **O(1) Get/Set**: Direct index access (1 ns)
- **O(1) Head/Last**: Instant access to ends
- **6x faster Prepend**: Optimized in-place shifting (1,021 ns vs Go's 6,218 ns)
- **Optimized Map**: Pre-allocated capacity (9,505 ns for 10k elements)
- **Good locality**: Better CPU cache utilization

**List Strengths:**
- **O(1) Prepend/Append**: Constant time at both ends (13-14 ns)
- **O(1) RemoveFirst/RemoveLast**: No shifting required (14-15 ns)
- **48% faster creation**: More efficient node allocation than Go's container/list
- **2.2x faster Map**: 133,013 ns vs Go's 294,430 ns
- **Stable iteration**: No reallocation during modification

**When to Use Each:**

| Scenario | Recommendation | Reason |
|----------|----------------|--------|
| Random access needed | Array | O(1) get/set |
| Building from end (append) | Array | O(1) amortized, cache locality |
| Building from front (prepend) | List | O(1), though Array is 6x faster than naive Go |
| Queue (FIFO) | List | O(1) at both ends, no shifting |
| Stack (LIFO) | Either | Both have O(1) push/pop |
| Large data processing | Array | Better cache locality |
| InsertAt/RemoveAt | Array* | Single copy vs traversal |
| Many Map/Filter operations | List | 2x faster than container/list |

*Array's O(n) InsertAt/RemoveAt uses efficient memory copy; List requires O(n) traversal first.

---

## Example: Building a Collection

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
)

func main() {
    // Build array with pre-allocated capacity
    var arr = ArrayWithCapacity[int](100)

    // Append elements
    for i := 0; i < 100; i++ {
        arr.Append(i * i)
    }

    // Transform: keep only values divisible by 4
    var filtered = arr.Filter((x int) => x % 4 == 0)

    // Sum all values
    var sum = filtered.FoldLeft[int](0, (acc int, x int) => acc + x)

    fmt.Printf("Sum of squares divisible by 4: %d\n", sum)
    fmt.Printf("Count: %d\n", filtered.Length())
}
```

## Example: Using List as Queue

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
)

func main() {
    // Create queue
    var queue = EmptyList[string]()

    // Enqueue (append to back)
    queue.Append("first")
    queue.Append("second")
    queue.Append("third")

    // Dequeue (remove from front)
    for queue.NonEmpty() {
        var item = queue.RemoveFirst()
        fmt.Printf("Processing: %s\n", item)
    }
}
```

## Example: Pattern Matching Pipeline

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
    . "martianoff/gala/std"
)

func categorize(n int) string {
    return n match {
        case x if x < 0 => "negative"
        case 0 => "zero"
        case x if x % 2 == 0 => "positive even"
        case _ => "positive odd"
    }
}

func main() {
    var numbers = ArrayOf[int](-5, -2, 0, 1, 2, 3, 4, 5)

    // Group by category using pattern matching
    var groups = numbers.GroupBy[string](categorize)

    // Print each group
    for category, arr := range groups {
        fmt.Printf("%s: %s\n", category, arr.String())
    }
}
```
