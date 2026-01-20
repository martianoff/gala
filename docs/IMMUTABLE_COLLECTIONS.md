# Immutable Collections

This document describes the immutable collection data structures available in GALA's `collection_immutable` package.

## Overview

The `collection_immutable` package provides persistent (immutable) data structures with performance characteristics matching [Scala's immutable collections](https://docs.scala-lang.org/overviews/collections/performance-characteristics.html).

### Import

```gala
import . "martianoff/gala/collection_immutable"
```

## Performance Characteristics

| Operation | List | Array |
|-----------|------|-------|
| Head | O(1) | O(eC) |
| Tail | O(1) | O(n) |
| Prepend | O(1) | O(1)* |
| Append | O(n) | O(eC) |
| Lookup | O(n) | O(eC) |
| Update | O(n) | O(eC) |
| Length | O(1) | O(1) |

**Legend:**
- O(1) - Constant time
- O(1)* - Amortized constant time (uses prefix buffer, consolidates every 32 prepends)
- O(n) - Linear time
- O(eC) - Effectively constant (O(log32 n) ≈ 7 operations for 1 billion elements)

---

## List[T]

An immutable singly-linked list. Best for stack-like operations (prepend, head, tail).

### Construction

```gala
// Empty list
val empty List[int] = Nil[int]()
val empty2 = EmptyList[int]()

// From elements
val list = ListOf[int](1, 2, 3, 4, 5)

// Using Cons (prepend constructor)
val list2 = Cons[int](1, Cons[int](2, Nil[int]()))
```

### Basic Operations

```gala
val list = ListOf[int](1, 2, 3, 4, 5)

list.IsEmpty()     // false
list.NonEmpty()    // true
list.Length()      // 5
list.Size()        // 5 (alias for Length)
```

### Head/Tail Operations

```gala
val list = ListOf[int](1, 2, 3)

// Head - first element
list.Head()              // 1
list.HeadOption()        // Some(1)

// Tail - all except first
list.Tail()              // List(2, 3)
list.TailOption()        // Some(List(2, 3))

// Last - last element
list.Last()              // 3
list.LastOption()        // Some(3)

// Init - all except last
list.Init()              // List(1, 2)
```

### Element Access

```gala
val list = ListOf[int](10, 20, 30)

list.Get(0)              // 10
list.Get(1)              // 20
list.GetOption(1)        // Some(20)
list.GetOption(10)       // None

// Update at index (returns new list)
list.Updated(1, 99)      // List(10, 99, 30)
```

### Adding Elements

```gala
val list = ListOf[int](2, 3, 4)

// Prepend - O(1)
list.Prepend(1)              // List(1, 2, 3, 4)

// PrependAll
list.PrependAll(ListOf[int](0, 1))  // List(0, 1, 2, 3, 4)

// Append - O(n)
list.Append(5)               // List(2, 3, 4, 5)

// AppendAll
list.AppendAll(ListOf[int](5, 6))   // List(2, 3, 4, 5, 6)
```

### Slicing Operations

```gala
val list = ListOf[int](1, 2, 3, 4, 5)

list.Take(3)                 // List(1, 2, 3)
list.Drop(2)                 // List(3, 4, 5)
list.TakeWhile((x int) => x < 4)  // List(1, 2, 3)
list.DropWhile((x int) => x < 3)  // List(3, 4, 5)
list.SplitAt(2)              // Tuple(List(1, 2), List(3, 4, 5))
```

### Searching

```gala
val list = ListOf[int](1, 2, 3, 4, 5)

list.Contains(3)             // true
list.IndexOf(3)              // 2
list.IndexOf(10)             // -1
list.Find((x int) => x > 3)  // Some(4)
```

### Transformations

```gala
val list = ListOf[int](1, 2, 3)

// Map
list.Map[int]((x int) => x * 2)  // List(2, 4, 6)

// FlatMap
list.FlatMap[int]((x int) => ListOf[int](x, x * 10))
// List(1, 10, 2, 20, 3, 30)

// Filter
list.Filter((x int) => x % 2 == 1)  // List(1, 3)
list.FilterNot((x int) => x % 2 == 1)  // List(2)

// Partition
list.Partition((x int) => x > 2)
// Tuple(List(3), List(1, 2))

// Reverse
list.Reverse()               // List(3, 2, 1)

// Distinct
ListOf[int](1, 2, 2, 3, 1).Distinct()  // List(1, 2, 3)
```

### Folding and Reduction

```gala
val list = ListOf[int](1, 2, 3, 4)

// FoldLeft
list.FoldLeft[int](0, (acc int, x int) => acc + x)  // 10

// FoldRight
list.FoldRight[int](0, (x int, acc int) => x + acc)  // 10

// Reduce
list.Reduce((a int, b int) => a + b)  // 10

// ReduceOption (safe for empty lists)
list.ReduceOption((a int, b int) => a + b)  // Some(10)
```

### Predicates

```gala
val list = ListOf[int](2, 4, 6, 8)

list.Exists((x int) => x == 4)  // true
list.ForAll((x int) => x % 2 == 0)  // true
list.Count((x int) => x > 4)  // 2
```

### Zipping

```gala
val nums = ListOf[int](1, 2, 3)
val strs = ListOf[string]("a", "b", "c")

nums.Zip[string](strs)
// List(Tuple(1, "a"), Tuple(2, "b"), Tuple(3, "c"))

nums.ZipWithIndex()
// List(Tuple(1, 0), Tuple(2, 1), Tuple(3, 2))
```

### Conversion

```gala
val list = ListOf[int](1, 2, 3)

list.ToSlice()  // []int{1, 2, 3}
list.String()   // "List(1, 2, 3)"
```

### Flattening Nested Lists

```gala
val nested = ListOf[List[int]](
    ListOf[int](1, 2),
    ListOf[int](3, 4),
)
Flatten[int](nested)  // List(1, 2, 3, 4)
```

### Pattern Matching

```gala
val list = ListOf[int](1, 2, 3)

val result = list match {
    case Cons(head, tail) => head  // Matches non-empty list
    case Nil() => -1               // Matches empty list
    case _ => -2
}
```

### ForEach (Side Effects)

```gala
list.ForEach((x int) => {
    fmt.Println(x)
})
```

---

## Array[T]

An immutable indexed sequence with effectively constant time for most operations. Best for random access and append operations.

### Construction

```gala
// Empty array
val empty Array[int] = EmptyArray[int]()

// From elements
val arr = ArrayOf[int](1, 2, 3, 4, 5)

// From slice
val slice = []int{1, 2, 3}
val arr2 = ArrayFrom[int](slice)
```

### Basic Operations

```gala
val arr = ArrayOf[int](1, 2, 3, 4, 5)

arr.IsEmpty()      // false
arr.NonEmpty()     // true
arr.Length()       // 5
arr.Size()         // 5
```

### Head/Last Operations

```gala
val arr = ArrayOf[int](1, 2, 3)

arr.Head()              // 1
arr.HeadOption()        // Some(1)
arr.Last()              // 3
arr.LastOption()        // Some(3)

arr.Tail()              // Array(2, 3)
arr.TailOption()        // Some(Array(2, 3))
arr.Init()              // Array(1, 2)
```

### Element Access - O(eC)

```gala
val arr = ArrayOf[int](10, 20, 30)

arr.Get(0)               // 10
arr.Get(1)               // 20
arr.GetOption(1)         // Some(20)
arr.GetOption(10)        // None

// Update at index (returns new array) - O(eC)
arr.Updated(1, 99)       // Array(10, 99, 30)
```

### Adding Elements

```gala
val arr = ArrayOf[int](2, 3, 4)

// Append - O(eC)
arr.Append(5)                // Array(2, 3, 4, 5)

// AppendAll
arr.AppendAll(ArrayOf[int](5, 6))   // Array(2, 3, 4, 5, 6)

// Prepend - O(1) amortized (uses prefix buffer)
arr.Prepend(1)               // Array(1, 2, 3, 4)

// PrependAll
arr.PrependAll(ArrayOf[int](0, 1))  // Array(0, 1, 2, 3, 4)
```

### Slicing Operations

```gala
val arr = ArrayOf[int](1, 2, 3, 4, 5)

arr.Take(3)                  // Array(1, 2, 3)
arr.Drop(2)                  // Array(3, 4, 5)
arr.Slice(1, 4)              // Array(2, 3, 4)
arr.TakeWhile((x int) => x < 4)   // Array(1, 2, 3)
arr.DropWhile((x int) => x < 3)   // Array(3, 4, 5)
arr.SplitAt(2)               // Tuple(Array(1, 2), Array(3, 4, 5))
```

### Searching

```gala
val arr = ArrayOf[int](1, 2, 3, 2, 1)

arr.Contains(3)              // true
arr.IndexOf(2)               // 1
arr.LastIndexOf(2)           // 3
arr.Find((x int) => x > 2)   // Some(3)
arr.FindLast((x int) => x < 3)  // Some(1)
```

### Transformations

```gala
val arr = ArrayOf[int](1, 2, 3)

// Map
arr.Map[int]((x int) => x * 2)  // Array(2, 4, 6)

// FlatMap
arr.FlatMap[int]((x int) => ArrayOf[int](x, x * 10))
// Array(1, 10, 2, 20, 3, 30)

// Filter
arr.Filter((x int) => x % 2 == 1)  // Array(1, 3)
arr.FilterNot((x int) => x % 2 == 1)  // Array(2)

// Partition
arr.Partition((x int) => x > 2)
// Tuple(Array(3), Array(1, 2))

// Reverse
arr.Reverse()                // Array(3, 2, 1)

// Distinct
ArrayOf[int](1, 2, 2, 3, 1).Distinct()  // Array(1, 2, 3)
```

### Folding and Reduction

```gala
val arr = ArrayOf[int](1, 2, 3, 4)

arr.FoldLeft[int](0, (acc int, x int) => acc + x)  // 10
arr.FoldRight[int](0, (x int, acc int) => x + acc)  // 10
arr.Reduce((a int, b int) => a + b)  // 10
arr.ReduceOption((a int, b int) => a + b)  // Some(10)
```

### Predicates

```gala
val arr = ArrayOf[int](2, 4, 6, 8)

arr.Exists((x int) => x == 4)  // true
arr.ForAll((x int) => x % 2 == 0)  // true
arr.Count((x int) => x > 4)  // 2
```

### Zipping

```gala
val nums = ArrayOf[int](1, 2, 3)
val strs = ArrayOf[string]("a", "b", "c")

nums.Zip[string](strs)
// Array(Tuple(1, "a"), Tuple(2, "b"), Tuple(3, "c"))

nums.ZipWithIndex()
// Array(Tuple(1, 0), Tuple(2, 1), Tuple(3, 2))
```

### Grouping

```gala
val arr = ArrayOf[int](1, 2, 3, 4, 5)

// Split into groups of size n
arr.Grouped(2)
// Array(Array(1, 2), Array(3, 4), Array(5))

// Sliding window
arr.Sliding(3)
// Array(Array(1, 2, 3), Array(2, 3, 4), Array(3, 4, 5))
```

### Conversion

```gala
val arr = ArrayOf[int](1, 2, 3)

arr.ToSlice()   // []int{1, 2, 3}
arr.ToList()    // List(1, 2, 3)
arr.String()    // "Array(1, 2, 3)"
```

### ForEach (Side Effects)

```gala
arr.ForEach((x int) => {
    fmt.Println(x)
})
```

---

## Choosing Between List and Array

| Use Case | Recommended |
|----------|-------------|
| Stack operations (push/pop from front) | List or Array |
| Random access by index | Array |
| Frequent appends to end | Array |
| Frequent prepends to front | List or Array |
| Recursive algorithms on sequences | List |
| Large sequences with updates | Array |

**Note:** With the prefix buffer optimization, Array now has O(1) amortized prepend, making it competitive with List for prepend-heavy workloads. Choose List when you need true O(1) without amortization, or Array when you also need random access.

### List Advantages
- O(1) prepend (cons)
- O(1) head and tail access
- Natural for recursive algorithms
- Efficient structural sharing for immutability

### Array Advantages
- O(eC) random access
- O(eC) append
- O(1) amortized prepend (using prefix buffer)
- O(eC) update at any position
- Better cache locality for iteration

---

## Implementation Details

### List
List is implemented as a classic persistent linked list (cons list). Each node contains a value and a pointer to the tail. This provides:
- Structural sharing: prepending creates a new node pointing to the existing list
- Cached length for O(1) size queries

### Array
Array is implemented as a 32-way branching trie (similar to Scala's Vector and Clojure's PersistentVector) with a prefix buffer for amortized O(1) prepend. This provides:
- Tree depth of at most 7 for up to 34 billion elements
- Path copying for updates, sharing unaffected subtrees
- Effectively constant time operations (O(log32 n))
- Prefix buffer: prepended elements are stored in a separate buffer until it reaches 32 elements, then consolidated into the tree (Scala-inspired optimization)

---

## Performance Benchmarks

Benchmark results comparing GALA immutable collections to Go native slices (immutable style with copy-on-write).

### Running the Benchmarks

```shell
# GALA immutable collections benchmark
bazel run //collection_immutable:perf_gala

# Go native slices benchmark (immutable style)
bazel run //collection_immutable:perf_go
```

### Results (ns/op) - 10,000 Elements

| Operation | GALA List | GALA Array | Go Slice (immutable) |
|-----------|----------:|-----------:|---------------------:|
| Creation | 124,000 |  3,459,000 | 34,890,000 |
| Prepend | 1 |          1 | 5,229 |
| Append | - |        458 | 7,443 |
| Head | 1 |          4 | 1 |
| Get(5000) | 5,035 |          4 | 0 |
| Update(5000) | - |        451 | 7,337 |
| Filter | 158,000 |     98,000 | 15,463 |
| Map | 263,000 |    148,000 | 10,476 |
| FoldLeft | 10,010 |     44,000 | 1,039 |
| Take(5000) | - |     64,000 | 515 |
| Drop(5000) | - |     66,000 | 1,044 |

### Scaling Results

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| List.Creation | 2,029 ns | 136,000 ns | 1,856,000 ns |
| Array.Creation | 15,926 ns | 3,443,000 ns | 52,713,000 ns |

**Notes:**
- GALA List uses Prepend (O(1)), GALA Array uses Append (O(eC))
- Go Slice (immutable): copy-on-write style, full copy on each modification
- `-` indicates operation not measured or not the primary use case
- Array uses optimized bulk building for Filter, Map, Take, Drop operations

### Key Performance Insights

**List Strengths:**
- **O(1) Prepend** (1 ns): Fastest way to build collections from the front
- **O(1) Head/Tail**: Efficient for stack-like patterns
- **Linear Creation**: Scales well (10x elements ≈ 10x time)

**Array Strengths:**
- **O(1) Prepend** (0 ns): Amortized constant time using prefix buffer (Scala-inspired)
- **O(eC) Append** (458 ns): 16x faster than immutable slice copy
- **O(eC) Get** (4 ns): Constant random access regardless of position
- **O(eC) Update** (451 ns): 16x faster than immutable slice copy
- **Bulk Operations Optimized**: Filter, Map, Take, Drop use bottom-up tree building

**Comparison to Go Immutable Slices:**

| Operation | GALA Array | Go Slice (copy) | GALA Advantage |
|-----------|----------:|----------------:|----------------|
| Creation(10k) | 3.5ms | 34.9ms | **10x faster** |
| Prepend | 0 ns | 5,229 ns | **∞ faster** (O(1) vs O(n)) |
| Append | 458 ns | 7,443 ns | **16x faster** |
| Update | 451 ns | 7,337 ns | **16x faster** |
| Get | 4 ns | 0 ns | ~same |

**When to Use Each:**

| Scenario | Recommendation |
|----------|----------------|
| Stack operations (LIFO) | List |
| Queue-like building (append) | Array |
| Random access needed | Array |
| Frequent updates | Array |
| Large collections with sharing | Array |
| Recursive algorithms | List |
| Simple iteration only | Go slice |

---

## Example: Building a Collection

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_immutable"
)

func main() {
    // Build a list of numbers
    val numbers = ListOf[int](1, 2, 3, 4, 5)

    // Transform: double each number
    val doubled = numbers.Map[int]((x int) => x * 2)

    // Filter: keep only values > 5
    val filtered = doubled.Filter((x int) => x > 5)

    // Reduce: sum all values
    val sum = filtered.Reduce((a int, b int) => a + b)

    fmt.Printf("Sum: %d\n", sum)  // Sum: 24 (6 + 8 + 10)

    // Original list is unchanged
    fmt.Println("Original:", numbers.String())  // List(1, 2, 3, 4, 5)
}
```

## Example: Using Array for Random Access

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_immutable"
)

func main() {
    // Build array of 1000 elements
    var arr Array[int] = EmptyArray[int]()
    for i := 0; i < 1000; i = i + 1 {
        arr = arr.Append(i * i)
    }

    // Random access - O(eC)
    fmt.Printf("Element at 500: %d\n", arr.Get(500))

    // Update - O(eC)
    val updated = arr.Updated(500, 999999)
    fmt.Printf("Updated element at 500: %d\n", updated.Get(500))

    // Original unchanged
    fmt.Printf("Original element at 500: %d\n", arr.Get(500))
}
```
