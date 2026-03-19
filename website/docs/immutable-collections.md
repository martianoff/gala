---
layout: default
title: "Immutable Collections in GALA - List, Array, HashMap, HashSet, TreeSet, TreeMap"
description: "GALA's immutable collection library. List, Array, HashMap, HashSet, TreeSet, and TreeMap with functional operations — Map, Filter, FoldLeft, Collect, and more. Full API reference."
keywords: "gala immutable collections, go immutable list, go immutable hashmap, golang functional collections, gala array, gala treemap, go persistent data structures"
permalink: /docs/immutable-collections/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/language-reference/">Docs</a> / Immutable Collections</p>

# Immutable Collections

This document describes the immutable collection data structures available in GALA's `collection_immutable` package.

---

## Table of Contents

1. [Overview](#overview)
2. [Performance Characteristics](#performance-characteristics)
3. [List[T]](#listt)
   - [Construction](#construction)
   - [Basic Operations](#basic-operations)
   - [Head/Tail Operations](#headtail-operations)
   - [Element Access](#element-access)
   - [Adding Elements](#adding-elements)
   - [Slicing Operations](#slicing-operations)
   - [Searching](#searching)
   - [Transformations](#transformations)
   - [Folding and Reduction](#folding-and-reduction)
   - [Pattern Matching](#pattern-matching)
4. [Array[T]](#arrayt)
   - [Construction](#construction-1)
   - [Element Access - O(eC)](#element-access---oec)
   - [Adding Elements](#adding-elements-1)
   - [Slicing Operations](#slicing-operations-1)
   - [Grouping](#grouping)
5. [HashSet[T]](#hashsett)
   - [Hashable Interface](#hashable-interface)
   - [Construction](#construction-2)
   - [Element Operations - O(eC)](#element-operations---oec)
   - [Set Operations](#set-operations)
6. [TreeSet[T]](#treesett)
   - [Ordered Interface](#ordered-interface)
   - [Min/Max Operations - O(log n)](#minmax-operations---olog-n)
   - [Range Operations](#range-operations---treeset-specific)
7. [HashMap[K,V]](#hashmapkv)
   - [Construction](#construction-3)
   - [Core Operations - O(eC)](#core-operations---oec)
   - [Functional Operations](#functional-operations)
8. [TreeMap[K,V]](#treemapkv)
   - [Construction](#construction-4)
   - [Core Operations - O(log n)](#core-operations---olog-n)
   - [Min/Max Operations](#minmax-operations)
   - [Range Queries](#range-queries)
   - [Functional Operations](#functional-operations-1)
   - [Conversion](#conversion)
9. [Choosing the Right Collection](#choosing-the-right-collection)
10. [Implementation Details](#implementation-details)
11. [Performance Benchmarks](#performance-benchmarks)
12. [Examples](#example-building-a-collection)

---

## Overview

The `collection_immutable` package provides persistent (immutable) data structures with performance characteristics matching [Scala's immutable collections](https://docs.scala-lang.org/overviews/collections/performance-characteristics.html).

### Import

```gala
import . "martianoff/gala/collection_immutable"
```

## Performance Characteristics

### Sequence Collections (List, Array)

| Operation | List | Array |
|-----------|------|-------|
| Head | O(1) | O(eC) |
| Tail | O(1) | O(n) |
| Prepend | O(1) | O(1)* |
| Append | O(n) | O(eC) |
| Lookup | O(n) | O(eC) |
| Update | O(n) | O(eC) |
| Contains | O(n) | O(n) |
| Length | O(1) | O(1) |

### Set Collections (HashSet, TreeSet)

| Operation | HashSet | TreeSet |
|-----------|---------|---------|
| Add | O(eC) | O(log n) |
| Remove | O(eC) | O(log n) |
| Contains | O(eC) | O(log n) |
| Min/Max | O(n) | O(log n) |
| Range | O(n) | O(log n + k) |
| Union | O(m) | O(m log n) |
| Intersect | O(m) | O(m log n) |
| Diff | O(n) | O(n log m) |
| Size | O(1) | O(1) |

### Map Collections (HashMap, TreeMap)

| Operation | HashMap | TreeMap |
|-----------|---------|---------|
| Put | O(eC) | O(log n) |
| Get | O(eC) | O(log n) |
| Remove | O(eC) | O(log n) |
| Contains | O(eC) | O(log n) |
| Min/Max Key | O(n) | O(log n) |
| Range | O(n) | O(log n + k) |
| Size | O(1) | O(1) |

**Legend:**
- O(1) - Constant time
- O(1)* - Amortized constant time (uses prefix buffer, consolidates every 32 prepends)
- O(n) - Linear time (n = this collection's size)
- O(m) - Linear in smaller set (m = min(this.size, other.size))
- O(eC) - Effectively constant (O(log32 n) ~ 7 operations for 1 billion elements)

---

## List[T]

An immutable singly-linked list. Best for stack-like operations (prepend, head, tail).

### Construction

```gala
// Empty list
val empty = EmptyList[int]()

// From elements
val list = ListOf(1, 2, 3, 4, 5)

// Using Prepend (creates new list with element at front)
val list2 = EmptyList[int]().Prepend(3).Prepend(2).Prepend(1)  // List(1, 2, 3)
```

### Basic Operations

```gala
val list = ListOf(1, 2, 3, 4, 5)

list.IsEmpty()     // false
list.NonEmpty()    // true
list.Length()      // 5
list.Size()        // 5 (alias for Length)
```

### Head/Tail Operations

```gala
val list = ListOf(1, 2, 3)

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
val list = ListOf(10, 20, 30)

list.Get(0)              // 10
list.Get(1)              // 20
list.GetOption(1)        // Some(20)
list.GetOption(10)       // None

// Update at index (returns new list)
list.Updated(1, 99)      // List(10, 99, 30)
```

### Adding Elements

```gala
val list = ListOf(2, 3, 4)

// Prepend - O(1)
list.Prepend(1)              // List(1, 2, 3, 4)

// PrependAll
list.PrependAll(ListOf(0, 1))  // List(0, 1, 2, 3, 4)

// Append - O(n)
list.Append(5)               // List(2, 3, 4, 5)

// AppendAll
list.AppendAll(ListOf(5, 6))   // List(2, 3, 4, 5, 6)
```

### Slicing Operations

```gala
val list = ListOf(1, 2, 3, 4, 5)

list.Take(3)                 // List(1, 2, 3)
list.Drop(2)                 // List(3, 4, 5)
list.TakeWhile((x) => x < 4)  // List(1, 2, 3)
list.DropWhile((x) => x < 3)  // List(3, 4, 5)
list.SplitAt(2)              // Tuple(List(1, 2), List(3, 4, 5))
```

### Searching

```gala
val list = ListOf(1, 2, 3, 4, 5)

list.Contains(3)             // true
list.IndexOf(3)              // 2
list.IndexOf(10)             // -1
list.Find((x) => x > 3)  // Some(4)
```

### Transformations

```gala
val list = ListOf(1, 2, 3)

// Map
list.Map((x) => x * 2)  // List(2, 4, 6)

// FlatMap
list.FlatMap((x) => ListOf(x, x * 10))
// List(1, 10, 2, 20, 3, 30)

// Filter
list.Filter((x) => x % 2 == 1)  // List(1, 3)
list.FilterNot((x) => x % 2 == 1)  // List(2)

// Collect - combines Filter and Map in a single pass using partial functions
list.Collect({ case x if x % 2 == 1 => x * 10 })
// List(10, 30)

// Partition
list.Partition((x) => x > 2)
// Tuple(List(3), List(1, 2))

// Reverse
list.Reverse()               // List(3, 2, 1)

// Distinct
ListOf(1, 2, 2, 3, 1).Distinct()  // List(1, 2, 3)
```

### Folding and Reduction

```gala
val list = ListOf(1, 2, 3, 4)

// FoldLeft
list.FoldLeft(0, (acc, x) => acc + x)  // 10

// FoldRight
list.FoldRight(0, (x, acc) => x + acc)  // 10

// Reduce
list.Reduce((a, b) => a + b)  // 10

// ReduceOption (safe for empty lists)
list.ReduceOption((a, b) => a + b)  // Some(10)
```

### Predicates

```gala
val list = ListOf(2, 4, 6, 8)

list.Exists((x) => x == 4)  // true
list.ForAll((x) => x % 2 == 0)  // true
list.Count((x) => x > 4)  // 2
```

### Zipping

```gala
val nums = ListOf(1, 2, 3)
val strs = ListOf("a", "b", "c")

nums.Zip(strs)
// List(Tuple(1, "a"), Tuple(2, "b"), Tuple(3, "c"))

nums.ZipWithIndex()
// List(Tuple(1, 0), Tuple(2, 1), Tuple(3, 2))
```

### Sorting

```gala
val list = ListOf("banana", "apple", "cherry")

list.Sorted()                              // List("apple", "banana", "cherry")
list.SortWith((a, b) => a > b)             // List("cherry", "banana", "apple")
list.SortBy((s) => len(s))                 // List("apple", "banana", "cherry")
```

### Conversion

```gala
val list = ListOf(1, 2, 3)

list.ToSlice()    // []int{1, 2, 3}
list.ToArray()    // Array(1, 2, 3)
list.String()     // "List(1, 2, 3)"
list.MkString(", ")  // "1, 2, 3"
```

### Flattening Nested Lists

```gala
val nested = ListOf(
    ListOf(1, 2),
    ListOf(3, 4),
)
Flatten[int](nested)  // List(1, 2, 3, 4)
```

### Pattern Matching

```gala
val list = ListOf(1, 2, 3)

val result = list match {
    case Cons(head, tail) => head  // Matches non-empty list
    case Nil() => -1               // Matches empty list
    case _ => -2
}
```

### ForEach (Side Effects)

```gala
list.ForEach((x) => {
    Println(x)
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
val arr = ArrayOf(1, 2, 3, 4, 5)

// From slice
val slice = SliceOf(1, 2, 3)
val arr2 = ArrayFrom(slice)

// Tabulate: compute each element from index (O(n) via arrayBuilder)
val squares = ArrayTabulate(5, (i) => i * i)
// squares == Array(0, 1, 4, 9, 16)

// Fill: repeat the same value (O(n) via arrayBuilder)
val zeros = ArrayFill(3, 0)
// zeros == Array(0, 0, 0)
```

### Basic Operations

```gala
val arr = ArrayOf(1, 2, 3, 4, 5)

arr.IsEmpty()      // false
arr.NonEmpty()     // true
arr.Length()       // 5
arr.Size()         // 5
```

### Head/Last Operations

```gala
val arr = ArrayOf(1, 2, 3)

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
val arr = ArrayOf(10, 20, 30)

arr.Get(0)               // 10
arr.Get(1)               // 20
arr.GetOption(1)         // Some(20)
arr.GetOption(10)        // None

// Update at index (returns new array) - O(eC)
arr.Updated(1, 99)       // Array(10, 99, 30)
```

### Adding Elements

```gala
val arr = ArrayOf(2, 3, 4)

// Append - O(eC)
arr.Append(5)                // Array(2, 3, 4, 5)

// AppendAll
arr.AppendAll(ArrayOf(5, 6))   // Array(2, 3, 4, 5, 6)

// Prepend - O(1) amortized (uses prefix buffer)
arr.Prepend(1)               // Array(1, 2, 3, 4)

// PrependAll
arr.PrependAll(ArrayOf(0, 1))  // Array(0, 1, 2, 3, 4)
```

### Slicing Operations

```gala
val arr = ArrayOf(1, 2, 3, 4, 5)

arr.Take(3)                  // Array(1, 2, 3)
arr.Drop(2)                  // Array(3, 4, 5)
arr.Slice(1, 4)              // Array(2, 3, 4)
arr.TakeWhile((x) => x < 4)   // Array(1, 2, 3)
arr.DropWhile((x) => x < 3)   // Array(3, 4, 5)
arr.SplitAt(2)               // Tuple(Array(1, 2), Array(3, 4, 5))
```

### Searching

```gala
val arr = ArrayOf(1, 2, 3, 2, 1)

arr.Contains(3)              // true
arr.IndexOf(2)               // 1
arr.LastIndexOf(2)           // 3
arr.Find((x) => x > 2)   // Some(3)
arr.FindLast((x) => x < 3)  // Some(1)
```

### Transformations

```gala
val arr = ArrayOf(1, 2, 3)

// Map
arr.Map((x) => x * 2)  // Array(2, 4, 6)

// FlatMap
arr.FlatMap((x) => ArrayOf(x, x * 10))
// Array(1, 10, 2, 20, 3, 30)

// Filter
arr.Filter((x) => x % 2 == 1)  // Array(1, 3)
arr.FilterNot((x) => x % 2 == 1)  // Array(2)

// Collect - combines Filter and Map in a single pass using partial functions
arr.Collect({ case x if x % 2 == 0 => x * 10 })
// Array(20)

// Partition
arr.Partition((x) => x > 2)
// Tuple(Array(3), Array(1, 2))

// Reverse
arr.Reverse()                // Array(3, 2, 1)

// Distinct
ArrayOf(1, 2, 2, 3, 1).Distinct()  // Array(1, 2, 3)
```

### Folding and Reduction

```gala
val arr = ArrayOf(1, 2, 3, 4)

arr.FoldLeft(0, (acc, x) => acc + x)  // 10
arr.FoldRight(0, (x, acc) => x + acc)  // 10
arr.Reduce((a, b) => a + b)  // 10
arr.ReduceOption((a, b) => a + b)  // Some(10)
```

### Predicates

```gala
val arr = ArrayOf(2, 4, 6, 8)

arr.Exists((x) => x == 4)  // true
arr.ForAll((x) => x % 2 == 0)  // true
arr.Count((x) => x > 4)  // 2
```

### Zipping

```gala
val nums = ArrayOf(1, 2, 3)
val strs = ArrayOf("a", "b", "c")

nums.Zip(strs)
// Array(Tuple(1, "a"), Tuple(2, "b"), Tuple(3, "c"))

nums.ZipWithIndex()
// Array(Tuple(1, 0), Tuple(2, 1), Tuple(3, 2))
```

### Grouping

```gala
val arr = ArrayOf(1, 2, 3, 4, 5)

// Split into groups of size n
arr.Grouped(2)
// Array(Array(1, 2), Array(3, 4), Array(5))

// Sliding window
arr.Sliding(3)
// Array(Array(1, 2, 3), Array(2, 3, 4), Array(3, 4, 5))
```

### Sorting

```gala
val arr = ArrayOf(3, 1, 4, 1, 5, 9)

// Natural ordering
arr.Sorted()                                // Array(1, 1, 3, 4, 5, 9)

// Custom comparator (descending)
arr.SortWith((a, b) => a > b)               // Array(9, 5, 4, 3, 1, 1)

// Sort by key function
val arr2 = ArrayOf(1, 9, 3, 7, 5)
arr2.SortBy((x) => x)                       // Array(1, 3, 5, 7, 9)
```

### Conversion

```gala
val arr = ArrayOf(1, 2, 3)

arr.ToSlice()       // []int{1, 2, 3}
arr.ToList()        // List(1, 2, 3)
arr.String()        // "Array(1, 2, 3)"
arr.MkString(", ")  // "1, 2, 3"
```

### ForEach (Side Effects)

```gala
arr.ForEach((x) => {
    Println(x)
})
```

---

## HashSet[T]

An immutable set with effectively constant time operations. Uses a Hash Array Mapped Trie (HAMT) structure similar to Scala's HashSet.

**Type Requirements:** T must be `comparable` and either:
- A **primitive type** (int, string, bool, float, etc.) - hashed automatically
- A **custom type** implementing the `Hashable` interface

### Hashable Interface

Custom types must implement the `Hashable` interface to be used in HashSet:

```gala
// The Hashable interface
type Hashable interface {
    Hash() uint32
}

// Example: Custom type implementing Hashable
type Person struct {
    Name string
    Age  int
}

func (p Person) Hash() uint32 {
    return HashCombine(HashString(p.Name), HashInt(int64(p.Age)))
}

// Now Person can be used in HashSet
val people = HashSetOf(
    Person(Name = "Alice", Age = 30),
    Person(Name = "Bob", Age = 25),
)
```

**Available hash helper functions:**
- `HashInt(n int64) uint32` - Hash integers
- `HashUint(n uint64) uint32` - Hash unsigned integers
- `HashString(s string) uint32` - Hash strings
- `HashBool(b bool) uint32` - Hash booleans
- `HashCombine(h1, h2 uint32) uint32` - Combine two hashes (for structs)

### Construction

```gala
// Empty set
val empty = EmptyHashSet[int]()

// From elements
val set = HashSetOf(1, 2, 3, 4, 5)

// From slice
val slice = SliceOf(1, 2, 3)
val set2 = HashSetFromSlice(slice)
```

### Element Operations - O(eC)

```gala
val set = HashSetOf(1, 2, 3)

// Add element (returns new set)
set.Add(4)               // HashSet(1, 2, 3, 4)

// Remove element (returns new set)
set.Remove(2)            // HashSet(1, 3)

// Check membership
set.Contains(2)          // true
set.Contains(10)         // false
```

### Set Operations

```gala
val a = HashSetOf(1, 2, 3, 4)
val b = HashSetOf(3, 4, 5, 6)

// Union - all elements from both sets
a.Union(b)               // HashSet(1, 2, 3, 4, 5, 6)

// Intersection - elements in both sets
a.Intersect(b)           // HashSet(3, 4)

// Difference - elements in a but not in b
a.Diff(b)                // HashSet(1, 2)

// Subset check
HashSetOf(1, 2).SubsetOf(a)  // true
```

---

## TreeSet[T]

An immutable sorted set implemented as a Red-Black tree. Maintains elements in sorted order and provides O(log n) operations with additional features like min/max and range queries.

**Type Requirements:** T must be `comparable` and either:
- A **primitive type** (int, string, float64, etc.) - compared automatically
- A **custom type** implementing the `Ordered[T]` interface

### Ordered Interface

Custom types must implement the `Ordered[T]` interface to be used in TreeSet:

```gala
// The Ordered interface
type Ordered[T any] interface {
    Compare(other T) int  // Returns -1, 0, or 1
}
```

### Min/Max Operations - O(log n)

TreeSet's main advantage over HashSet: efficient min/max access.

```gala
val set = TreeSetOf(5, 3, 1, 4, 2)

set.Min()                // 1
set.MinOption()          // Some(1)
set.Max()                // 5
set.MaxOption()          // Some(5)
```

### Range Operations - TreeSet-specific

```gala
val set = TreeSetOf(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

// Range [from, to] inclusive
set.Range(3, 7)          // TreeSet(3, 4, 5, 6, 7)

// Range from (>= value)
set.RangeFrom(7)         // TreeSet(7, 8, 9, 10)

// Range to (<= value)
set.RangeTo(4)           // TreeSet(1, 2, 3, 4)
```

---

## HashMap[K,V]

An immutable hash-based map with effectively constant time operations.

### Construction

```gala
val empty = EmptyHashMap[string, int]()
val m = HashMapOf(("a", 1), ("b", 2), ("c", 3))
```

### Core Operations - O(eC)

```gala
val m = HashMapOf(("a", 1), ("b", 2))

m.Put("c", 3)           // new map with added entry
m.Get("a")              // Some(1)
m.GetOrElse("z", 0)     // 0
m.Contains("a")         // true
m.Remove("b")           // new map without "b"
m.Size()                // 2
```

### Functional Operations

```gala
val m = HashMapOf(("a", 1), ("b", 2), ("c", 3))

m.Filter((k, v) => v > 1)                    // HashMap(b -> 2, c -> 3)
m.MapValues((v) => v * 2)                    // HashMap(a -> 2, b -> 4, c -> 6)
m.FoldLeftKV(0, (acc, k, v) => acc + v)      // 6
m.Sorted()                                    // Array((a,1), (b,2), (c,3))

// Collect - applies a partial function to each entry, returns Array[U]
m.Collect({ case (k, v) if v > 1 => k })
// Array("b", "c")
```

---

## TreeMap[K,V]

An immutable sorted map implemented as a Red-Black tree. Maintains entries in sorted key order and provides O(log n) operations with range queries.

### Construction

```gala
val empty = EmptyTreeMap[string, int]()
val m = TreeMapOf(("cherry", 3), ("apple", 1), ("banana", 2))
// TreeMap(apple -> 1, banana -> 2, cherry -> 3)  -- sorted by key
```

### Core Operations - O(log n)

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

val m2 = m.Put("d", 4)
m.Get("b")              // Some(2)
m.GetOrElse("z", 0)     // 0
m.Contains("a")         // true
val m3 = m.Remove("b")  // TreeMap(a -> 1, c -> 3)
m.Size()                // 3
```

### Min/Max Operations

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

m.MinKey()              // "a"
m.MaxKey()              // "c"
m.MinEntry()            // Tuple("a", 1)
m.MaxEntry()            // Tuple("c", 3)
```

### Range Queries

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3), ("d", 4), ("e", 5))

m.Range("b", "d")       // TreeMap(b -> 2, c -> 3, d -> 4)
m.RangeFrom("c")        // TreeMap(c -> 3, d -> 4, e -> 5)
m.RangeTo("c")          // TreeMap(a -> 1, b -> 2, c -> 3)
```

---

## Choosing the Right Collection

| Use Case | Recommended |
|----------|-------------|
| Stack operations (push/pop from front) | List or Array |
| Random access by index | Array |
| Frequent appends to end | Array |
| Frequent prepends to front | List or Array |
| Recursive algorithms on sequences | List |
| Large sequences with updates | Array |
| Fast membership testing | HashSet |
| Set operations (union, intersection) | HashSet or TreeSet |
| Unique elements collection | HashSet or TreeSet |
| Sorted iteration needed | TreeSet or TreeMap |
| Need min/max of set | TreeSet |
| Range queries (elements between X and Y) | TreeSet or TreeMap |
| Key-value lookup (unordered) | HashMap |
| Key-value lookup (sorted keys) | TreeMap |
| Sorted key-value iteration | TreeMap |
| Min/max key access | TreeMap |
| Range queries on keys | TreeMap |

---

## Implementation Details

### List
List is implemented as a classic persistent linked list (cons list). Each node contains a value and a pointer to the tail. This provides structural sharing and cached length for O(1) size queries.

### Array
Array is implemented as a 32-way branching trie (similar to Scala's Vector and Clojure's PersistentVector) with Scala-inspired optimizations including a prefix buffer for O(1) amortized prepend.

### HashSet
HashSet is implemented as a Hash Array Mapped Trie (HAMT), similar to Scala's HashSet, with 32-way branching trie with bitmap indexing.

### TreeSet
TreeSet is implemented as a persistent Red-Black tree, similar to Scala's TreeSet, with O(log n) operations and in-order traversal for sorted iteration.

### HashMap
HashMap is implemented as a Hash Array Mapped Trie (HAMT), similar to Scala's HashMap, storing key-value pairs.

### TreeMap
TreeMap is implemented as a persistent Red-Black tree, similar to Scala's TreeMap, storing key-value pairs with O(log n) operations.

---

## Example: Building a Collection

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    // Build a list of numbers
    val numbers = ListOf(1, 2, 3, 4, 5)

    // Transform: double each number
    val doubled = numbers.Map((x) => x * 2)

    // Filter: keep only values > 5
    val filtered = doubled.Filter((x) => x > 5)

    // Reduce: sum all values
    val sum = filtered.Reduce((a, b) => a + b)

    Println(s"Sum: $sum")  // Sum: 24 (6 + 8 + 10)

    // Original list is unchanged
    Println(s"Original: ${numbers.String()}")  // List(1, 2, 3, 4, 5)
}
```

## Example: Using Array for Random Access

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    // Build array of 1000 elements
    var arr Array[int] = EmptyArray[int]()
    for i := 0; i < 1000; i = i + 1 {
        arr = arr.Append(i * i)
    }

    // Random access - O(eC)
    Println(s"Element at 500: ${arr.Get(500)}")

    // Update - O(eC)
    val updated = arr.Updated(500, 999999)
    Println(s"Updated element at 500: ${updated.Get(500)}")

    // Original unchanged
    Println(s"Original element at 500: ${arr.Get(500)}")
}
```

---

See also: [Mutable Collections](/docs/mutable-collections/) | [Streams](/docs/streams/) | [Language Reference](/docs/language-reference/) | [Code Examples](/docs/examples/)
