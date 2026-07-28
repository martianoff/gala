---
layout: default
title: "Mutable Collections in GALA - Performance-Optimized Data Structures"
description: "Mutable collection variants in GALA for performance-sensitive code. Mutable Array, List, HashMap, HashSet with in-place operations alongside functional immutable collections."
keywords: "gala mutable collections, go mutable array, golang mutable hashmap, gala performance collections"
permalink: /docs/mutable-collections/
last_modified_at: 2026-04-19
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Mutable Collections</p>

# Mutable Collections

This document describes the mutable collection data structures available in GALA's `collection_mutable` package.

---

## Overview

The `collection_mutable` package provides mutable data structures optimized for in-place modifications. These collections are ideal when you need to build up data incrementally or when immutability overhead is unacceptable.

### Import

```gala
import . "martianoff/gala/collection_mutable"
```

## Performance Characteristics

### Sequences

| Operation | Array | List | Notes |
|-----------|-------|------|-------|
| Get/Set | O(1) | O(n) | Array: direct index; List: traversal |
| Head/Last | O(1) | O(1) | |
| Append | O(1)* | O(1) | Array uses Go slice growth |
| Prepend | O(n) | O(1) | Array shifts all elements |
| RemoveFirst | O(1) | O(1) | |
| RemoveLast | O(1) | O(1) | |
| Length | O(1) | O(1) | |

### Sets

| Operation | HashSet | TreeSet | Notes |
|-----------|---------|---------|-------|
| Add | O(1)* | O(log n) | HashSet: amortized; TreeSet: balanced |
| Remove | O(1) | O(log n) | |
| Contains | O(1) | O(log n) | |
| Min/Max | O(n) | O(log n) | TreeSet maintains sorted order |

### Maps

| Operation | HashMap | TreeMap | Notes |
|-----------|---------|---------|-------|
| Put | O(1)* | O(log n) | HashMap: amortized; TreeMap: balanced |
| Get | O(1) | O(log n) | |
| Remove | O(1) | O(log n) | |
| Contains | O(1) | O(log n) | |
| Min/Max Key | O(n) | O(log n) | TreeMap maintains sorted key order |

---

## Array[T]

A mutable indexed sequence backed by a Go slice. Best for random access and append operations.

### Construction

```gala
// Empty array
var arr = EmptyArray[int]()

// From elements (pre-allocates capacity)
var arr = ArrayOf(1, 2, 3, 4, 5)

// From slice (copies the slice)
var slice = SliceOf(1, 2, 3)
var arr = ArrayFromSlice(slice)

// With pre-allocated capacity (for known sizes)
var arr = ArrayWithCapacity[int](1000)

// Tabulate: compute each element from index
var squares = ArrayTabulate(5, (i) => i * i)
// squares == Array(0, 1, 4, 9, 16)

// Fill: repeat the same value
var zeros = ArrayFill(3, 0)
// zeros == Array(0, 0, 0)
```

### Element Access - O(1)

```gala
var arr = ArrayOf(10, 20, 30)

arr.Get(0)               // 10
arr.GetOption(1)         // Some(20)
arr.GetOption(10)        // None

arr.Head()               // 10
arr.HeadOption()         // Some(10)
arr.Last()               // 30
arr.LastOption()         // Some(30)
```

### Mutation Operations

```gala
var arr = ArrayOf(1, 2, 3)

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
arr.RemoveFirst()        // returns 0
arr.RemoveLast()         // returns 4

// Clear
arr.Clear()

// Reverse in-place
arr.Reverse()
```

### Functional Operations

```gala
var arr = ArrayOf(1, 2, 3, 4, 5)

// Map - returns new Array
var doubled = arr.Map((x) => x * 2)  // Array(2, 4, 6, 8, 10)

// Filter - returns new Array
var evens = arr.Filter((x) => x % 2 == 0)  // Array(2, 4)

// Collect - combines Filter and Map in a single pass
arr.Collect({ case x if x % 2 == 0 => x * 10 })

// FoldLeft
var sum = arr.FoldLeft(0, (acc, x) => acc + x)  // 15
```

### Sorting

Sorting returns a new `*Array[T]`, leaving the original unchanged.

```gala
var arr = ArrayOf(3, 1, 4, 1, 5, 9)

arr.Sorted()                                // Array(1, 1, 3, 4, 5, 9)
arr.SortWith((a, b) => a > b)               // Array(9, 5, 4, 3, 1, 1)
arr.SortBy((x) => x)                        // Array(1, 1, 3, 4, 5, 9)
```

---

## List[T]

A mutable doubly-linked list. Best for frequent insertions/removals at both ends.

### Construction

```gala
var list = EmptyList[int]()
var list = ListOf(1, 2, 3, 4, 5)
```

### Mutation Operations - O(1) at ends

```gala
var list = ListOf(2, 3, 4)

list.Prepend(1)          // list is now [1, 2, 3, 4]
list.Append(5)           // list is now [1, 2, 3, 4, 5]
list.RemoveFirst()       // returns 1
list.RemoveLast()        // returns 5
list.Clear()
list.Reverse()
```

### Functional Operations

```gala
var list = ListOf(1, 2, 3, 4, 5)

var doubled = list.Map((x) => x * 2)
var evens = list.Filter((x) => x % 2 == 0)
var sum = list.FoldLeft(0, (acc, x) => acc + x)
```

---

## HashSet[T]

A mutable hash-based set providing O(1) average-case operations.

### Construction

```gala
var s = EmptyHashSet[int]()
var s = HashSetOf(1, 2, 3, 4, 5)
```

### Membership and Modification - O(1)

```gala
var s = HashSetOf(1, 2, 3)

s.Contains(2)           // true
s.Add(4)                // true (was added)
s.Remove(2)             // true (was removed)
s.Clear()
```

### Set Operations

```gala
var s1 = HashSetOf(1, 2, 3)
var s2 = HashSetOf(2, 3, 4)

// Return new sets
var union = s1.Union(s2)           // HashSet(1, 2, 3, 4)
var inter = s1.Intersect(s2)       // HashSet(2, 3)
var diff = s1.Diff(s2)             // HashSet(1)
var symDiff = s1.SymmetricDiff(s2) // HashSet(1, 4)

// In-place modification
s1.UnionInPlace(s2)
s1.IntersectInPlace(s2)
s1.DiffInPlace(s2)
```

### Set Relationships

```gala
var s1 = HashSetOf(1, 2)
var s2 = HashSetOf(1, 2, 3, 4)

s1.SubsetOf(s2)          // true
s2.SupersetOf(s1)        // true

var s3 = HashSetOf(5, 6)
s1.Disjoint(s3)          // true
```

---

## TreeSet[T]

A mutable sorted set implemented as a Red-Black tree. Maintains elements in sorted order and provides O(log n) operations.

### Min/Max Operations - O(log n)

```gala
var s = TreeSetOf(5, 3, 7, 1, 9)

s.Min()                  // 1
s.Max()                  // 9

// Pop (remove and return)
s.PopMin()               // 1, s is now TreeSet(3, 5, 7, 9)
s.PopMax()               // 9, s is now TreeSet(3, 5, 7)
```

### Range Queries (unique to TreeSet)

```gala
var s = TreeSetOf(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

var range1 = s.Range(3, 7)     // TreeSet(3, 4, 5, 6, 7)
var range2 = s.RangeFrom(7)    // TreeSet(7, 8, 9, 10)
var range3 = s.RangeTo(4)      // TreeSet(1, 2, 3, 4)
```

---

## HashMap[K,V]

A mutable hash-based map providing O(1) average-case operations.

### Core Operations - O(1)

```gala
var m = HashMapOf(("a", 1), ("b", 2))

m.Put("c", 3)            // adds entry in-place
m.Get("a")               // Some(1)
m.GetOrElse("z", 0)      // 0
m.Contains("a")          // true
m.Remove("b")            // removes in-place
m.Size()                 // 2
m.Clear()

m.Filter((k, v) => v > 1)
m.MapValues((v) => v * 2)
m.Sorted()               // Array of entries sorted by key
```

---

## TreeMap[K,V]

A mutable sorted map implemented as a Red-Black tree. Maintains entries in sorted key order and provides O(log n) operations with range queries.

### Core Operations - O(log n)

```gala
var m = EmptyTreeMap[string, int]()

m.Put("a", 1)            // true
m.Put("a", 10)           // false (updated existing)
m.Get("a")               // Some(10)
m.GetOrElse("z", 0)      // 0
m.Contains("a")          // true
m.Remove("a")
```

### Min/Max Operations

```gala
var m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

m.MinKey()               // "a"
m.MaxKey()               // "c"
m.MinEntry()             // Tuple("a", 1)
m.MaxEntry()             // Tuple("c", 3)
```

### Range Queries

```gala
var m = TreeMapOf(("a", 1), ("b", 2), ("c", 3), ("d", 4), ("e", 5))

m.Range("b", "d")        // TreeMap(b -> 2, c -> 3, d -> 4)
m.RangeFrom("c")         // TreeMap(c -> 3, d -> 4, e -> 5)
m.RangeTo("c")           // TreeMap(a -> 1, b -> 2, c -> 3)
```

### Mutable-Specific Operations

```gala
var m = TreeMapOf(("a", 1), ("b", 2))

m.PutIfAbsent("a", 99)   // no change
m.PutIfAbsent("c", 3)    // inserts

val min = m.PopMinEntry() // removes and returns smallest
val max = m.PopMaxEntry() // removes and returns largest

m.Merge(other, (v1, v2) => v1 + v2)
m.FilterInPlace((k, v) => v > 2)
```

---

## Choosing Between HashSet and TreeSet

| Use Case | Recommended | Reason |
|----------|-------------|--------|
| Fast membership testing | HashSet | O(1) vs O(log n) |
| Sorted iteration needed | TreeSet | Maintains order |
| Min/Max queries | TreeSet | O(log n) vs O(n) |
| Range queries | TreeSet | Only TreeSet supports |
| Set operations (union, intersect) | HashSet | Faster on average |
| Priority queue semantics | TreeSet | PopMin/PopMax |

## Choosing Between HashMap and TreeMap

| Use Case | Recommended | Reason |
|----------|-------------|--------|
| Fastest put/get/remove | HashMap | O(1) vs O(log n) |
| Sorted key iteration | TreeMap | Maintains order |
| Min/Max key queries | TreeMap | O(log n) vs O(n) |
| Range queries on keys | TreeMap | Only TreeMap supports |
| Key order doesn't matter | HashMap | Faster on average |

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

---

## Pattern Matching with Mutable Collections

GALA supports pattern matching on mutable collections using type matching and guards.

```gala
func describeArray[T any](arr *Array[T]) string {
    return arr match {
        case a: *Array[_] if a.IsEmpty() => "empty array"
        case a: *Array[_] if a.Length() == 1 => s"single element: ${a.Head()}"
        case a: *Array[_] => s"array with ${a.Length()} elements"
        case _ => "unknown"
    }
}
```

### Option Pattern Matching with Collections

```gala
func findFirstEven(arr *Array[int]) string {
    var found = arr.Find((x) => x % 2 == 0)
    return found match {
        case Some(value) => s"found even: $value"
        case None() => "no even numbers"
    }
}
```

---

## Performance Benchmarks

### Array vs Go Slice (ns/op) - 10,000 Elements

| Operation | GALA Array | Go Slice | Notes |
|-----------|----------:|----------:|-------|
| Creation | 36,910 | 29,562 | ~same |
| Append | 1 | 1 | Both O(1) amortized |
| Prepend | 1,028 | 6,701 | **GALA 6.5x faster** |
| Get(5000) | 1 | 0 | Both O(1) |
| Set(5000) | 1 | 1 | Both O(1) |
| Filter | 20,611 | 14,536 | ~same |
| Map | 8,734 | 7,500 | ~same |
| FoldLeft | 951 | 972 | ~same |

### List vs Go container/list (ns/op) - 10,000 Elements

| Operation | GALA List | Go List | Notes |
|-----------|----------:|--------:|-------|
| Creation | 119,868 | 230,021 | **GALA 48% faster** |
| Prepend | 13 | 19 | **GALA 32% faster** |
| Append | 14 | 20 | **GALA 30% faster** |
| Map | 133,013 | 294,430 | **GALA 2.2x faster** |

### Running the Benchmarks

```shell
bazel run //collection_mutable:perf_gala
bazel run //collection_mutable:perf_go
```

---

## Example: Building a Collection

```gala
package main

import (
    . "martianoff/gala/collection_mutable"
)

func main() {
    var arr = ArrayWithCapacity[int](100)

    for i := 0; i < 100; i++ {
        arr.Append(i * i)
    }

    var filtered = arr.Filter((x) => x % 4 == 0)
    var sum = filtered.FoldLeft(0, (acc, x) => acc + x)

    Println(s"Sum of squares divisible by 4: $sum")
    Println(s"Count: ${filtered.Length()}")
}
```

## Example: Using List as Queue

```gala
package main

import (
    . "martianoff/gala/collection_mutable"
)

func main() {
    var queue = EmptyList[string]()

    queue.Append("first")
    queue.Append("second")
    queue.Append("third")

    for queue.NonEmpty() {
        var item = queue.RemoveFirst()
        Println(s"Processing: $item")
    }
}
```

## Example: Using TreeSet as Priority Queue

```gala
package main

import (
    . "martianoff/gala/collection_mutable"
)

func main() {
    var tasks = EmptyTreeSet[int]()

    tasks.Add(5)
    tasks.Add(1)
    tasks.Add(3)
    tasks.Add(2)

    for tasks.NonEmpty() {
        var priority = tasks.PopMin()
        Println(s"Processing task with priority $priority")
    }
}
```

---

See also: [Immutable Collections](/docs/immutable-collections/) | [Streams](/docs/streams/) | [Language Reference](/docs/language-reference/) | [Code Examples](/docs/examples/)
