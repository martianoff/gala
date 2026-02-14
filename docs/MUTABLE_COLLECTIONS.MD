# Mutable Collections

This document describes the mutable collection data structures available in GALA's `collection_mutable` package.

---

## Table of Contents

1. [Overview](#overview)
2. [Performance Characteristics](#performance-characteristics)
3. [Array[T]](#arrayt)
   - [Construction](#construction)
   - [Basic Operations](#basic-operations)
   - [Element Access - O(1)](#element-access---o1)
   - [Mutation Operations](#mutation-operations)
   - [Bulk Operations](#bulk-operations)
   - [Structural Operations](#structural-operations-return-new-arrays)
   - [Functional Operations](#functional-operations)
4. [List[T]](#listt)
   - [Construction](#construction-1)
   - [Element Access](#element-access)
   - [Mutation Operations - O(1) at ends](#mutation-operations---o1-at-ends)
5. [HashSet[T]](#hashsett)
   - [Type Requirements](#type-requirements)
   - [Construction](#construction-2)
   - [Membership and Modification - O(1)](#membership-and-modification---o1)
   - [Set Operations (return new HashSet)](#set-operations-return-new-hashset)
   - [Set Operations (in-place modification)](#set-operations-in-place-modification)
6. [TreeSet[T]](#treesett)
   - [Type Requirements](#type-requirements-1)
   - [Membership and Modification - O(log n)](#membership-and-modification---olog-n)
   - [Min/Max Operations - O(log n)](#minmax-operations---olog-n)
   - [Range Queries (unique to TreeSet)](#range-queries-unique-to-treeset)
7. [HashMap[K,V]](#hashmapkv)
   - [Construction](#construction-3)
   - [Core Operations - O(1)](#core-operations---o1)
8. [TreeMap[K,V]](#treemapkv)
   - [Construction](#construction-4)
   - [Core Operations - O(log n)](#core-operations---olog-n)
   - [Min/Max Operations](#minmax-operations)
   - [Range Queries](#range-queries)
   - [Mutable-Specific Operations](#mutable-specific-operations)
   - [Functional Operations](#functional-operations-1)
   - [Conversion](#conversion)
9. [Choosing Between HashSet and TreeSet](#choosing-between-hashset-and-treeset)
10. [Choosing Between HashMap and TreeMap](#choosing-between-hashmap-and-treemap)
11. [Pattern Matching with Mutable Collections](#pattern-matching-with-mutable-collections)
12. [Choosing Between Array and List](#choosing-between-array-and-list)
13. [Performance Benchmarks](#performance-benchmarks)
14. [Examples](#example-building-a-collection)

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
| AppendAll | O(m)* | O(m) | m = elements added |
| Prepend | O(n) | O(1) | Array shifts all elements |
| PrependAll | O(n+m) | O(m) | |
| Insert | O(n) | O(n) | |
| RemoveAt | O(n) | O(n) | Array shifts; List traverses |
| RemoveFirst | O(1) | O(1) | Array: slice view |
| RemoveLast | O(1) | O(1) | Array: slice view; List: O(1) with tail ptr |
| Length | O(1) | O(1) | |

### Sets

| Operation | HashSet | TreeSet | Notes |
|-----------|---------|---------|-------|
| Add | O(1)* | O(log n) | HashSet: amortized; TreeSet: balanced |
| Remove | O(1) | O(log n) | |
| Contains | O(1) | O(log n) | |
| Min/Max | O(n) | O(log n) | TreeSet maintains sorted order |
| Size | O(1) | O(1) | Cached |
| Union | O(m) | O(m log n) | m = size of other set |
| Intersect | O(min(n,m)) | O(min(n,m) log n) | |

### Maps

| Operation | HashMap | TreeMap | Notes |
|-----------|---------|---------|-------|
| Put | O(1)* | O(log n) | HashMap: amortized; TreeMap: balanced |
| Get | O(1) | O(log n) | |
| Remove | O(1) | O(log n) | |
| Contains | O(1) | O(log n) | |
| Min/Max Key | O(n) | O(log n) | TreeMap maintains sorted key order |
| Range | O(n) | O(log n + k) | k = result size |
| Size | O(1) | O(1) | Cached |

**Legend:**
- O(1) - Constant time
- O(1)* - Amortized constant time (may occasionally reallocate)
- O(n) - Linear time in current size
- O(m) - Linear time in elements being added
- O(log n) - Logarithmic time (balanced tree)

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

// Tabulate: compute each element from index (pre-allocates capacity)
var squares = ArrayTabulate(5, (i int) => i * i)
// squares == Array(0, 1, 4, 9, 16)

// Fill: repeat the same value (pre-allocates capacity)
var zeros = ArrayFill(3, 0)
// zeros == Array(0, 0, 0)
```

### Basic Operations

```gala
var arr = ArrayOf(1, 2, 3, 4, 5)

arr.IsEmpty()      // false
arr.NonEmpty()     // true
arr.Length()       // 5
arr.Size()         // 5 (alias for Length)
arr.Capacity()     // >= 5
```

### Element Access - O(1)

```gala
var arr = ArrayOf(10, 20, 30)

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
arr.RemoveFirst()        // returns 0, arr is now [1, 99, 3, 4]
arr.RemoveLast()         // returns 4, arr is now [1, 99, 3]

// Clear
arr.Clear()              // arr is now []

// Reverse in-place
arr.Reverse()
```

### Bulk Operations

```gala
var arr = ArrayOf(1, 2, 3)

// Append from a slice - O(m) amortized
var more = SliceOf(4, 5, 6)
arr.AppendAll(more)

// Append from another Array - O(m) amortized
var other = ArrayOf(7, 8, 9)
arr.AppendFrom(other)

// Prepend from a slice - O(n+m)
arr.PrependAll(SliceOf(-2, -1, 0))
```

### Structural Operations (return new arrays)

```gala
var arr = ArrayOf(1, 2, 3, 4, 5)

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
var arr = ArrayOf(1, 2, 3, 4, 5)

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
var arr = ArrayOf(1, 2, 3, 2, 1)

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
var arr = ArrayOf(1, 2, 3, 4, 5)

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
var arr = ArrayOf(1, 2, 2, 3, 3, 3)
arr.Distinct()  // Array(1, 2, 3)
```

### Sorting

Sorting returns a new `*Array[T]`, leaving the original unchanged.

```gala
var arr = ArrayOf(3, 1, 4, 1, 5, 9)

// Natural ordering
arr.Sorted()                                // Array(1, 1, 3, 4, 5, 9)

// Custom comparator (descending)
arr.SortWith((a, b) => a > b)               // Array(9, 5, 4, 3, 1, 1)

// Sort by key function
arr.SortBy((x) => x)                        // Array(1, 1, 3, 4, 5, 9)
```

### Conversion

```gala
var arr = ArrayOf(1, 2, 3)

arr.ToSlice()       // []int{1, 2, 3}
arr.ToList()        // List(1, 2, 3)
arr.String()        // "Array(1, 2, 3)"
arr.MkString(", ")  // "1, 2, 3"
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
var list = ListOf(1, 2, 3, 4, 5)

// From slice
var slice = SliceOf(1, 2, 3)
var list = ListFromSlice(slice)
```

### Basic Operations

```gala
var list = ListOf(1, 2, 3, 4, 5)

list.IsEmpty()     // false
list.NonEmpty()    // true
list.Length()      // 5
list.Size()        // 5
```

### Element Access

```gala
var list = ListOf(10, 20, 30)

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
var list = ListOf(2, 3, 4)

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
var list = ListOf(1, 2, 3)

// Prepend multiple
list.PrependAll(ListOf(-1, 0))  // [-1, 0, 1, 2, 3]

// Append multiple
list.AppendAll(ListOf(4, 5))    // [-1, 0, 1, 2, 3, 4, 5]
```

### Functional Operations

```gala
var list = ListOf(1, 2, 3, 4, 5)

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
var list = ListOf(1, 2, 3, 2, 1)

list.Contains(3)              // true
list.IndexOf(2)               // 1
list.LastIndexOf(2)           // 3
list.Find((x int) => x > 2)   // Some(3)
list.Exists((x int) => x > 4) // true
list.ForAll((x int) => x > 0) // true
list.Count((x int) => x == 2) // 2
```

### Sorting

Sorting returns a new `*List[T]`.

```gala
var list = ListOf("banana", "apple", "cherry")

list.Sorted()                              // List("apple", "banana", "cherry")
list.SortWith((a, b) => a > b)             // List("cherry", "banana", "apple")
list.SortBy((s) => len(s))                 // List("apple", "banana", "cherry")
```

### Conversion

```gala
var list = ListOf(1, 2, 3)

list.ToSlice()       // []int{1, 2, 3}
list.ToArray()       // Array(1, 2, 3)
list.String()        // "List(1, 2, 3)"
list.MkString(", ")  // "1, 2, 3"
```

---

## HashSet[T]

A mutable hash-based set providing O(1) average-case operations. Best for fast membership testing and set operations.

### Type Requirements

Elements must be either:
- **Primitive types**: `int`, `string`, `float64`, `bool`, etc. (handled automatically)
- **Types implementing `Hashable`**: Custom types that provide a `Hash() uint32` method

```gala
import . "martianoff/gala/std"

// Example custom type implementing Hashable
type Point struct {
    X int
    Y int
}

func (p Point) Hash() uint32 {
    return HashInt(int64(p.X)) ^ HashInt(int64(p.Y))
}
```

### Construction

```gala
// Empty set
var s = EmptyHashSet[int]()

// From elements
var s = HashSetOf(1, 2, 3, 4, 5)

// From slice
var slice = SliceOf(1, 2, 3)
var s = HashSetFromSlice(slice)
```

### Basic Operations

```gala
var s = HashSetOf(1, 2, 3, 4, 5)

s.IsEmpty()      // false
s.NonEmpty()     // true
s.Size()         // 5
s.Length()       // 5 (alias)
```

### Membership and Modification - O(1)

```gala
var s = HashSetOf(1, 2, 3)

// Contains - O(1)
s.Contains(2)           // true
s.Contains(5)           // false

// Add - O(1) amortized
s.Add(4)                // true (was added)
s.Add(2)                // false (already present)

// Remove - O(1)
s.Remove(2)             // true (was removed)
s.Remove(10)            // false (not present)

// Clear
s.Clear()               // removes all elements
```

### Set Operations (return new HashSet)

```gala
var s1 = HashSetOf(1, 2, 3)
var s2 = HashSetOf(2, 3, 4)

// Union: elements in either set
var union = s1.Union(s2)           // HashSet(1, 2, 3, 4)

// Intersection: elements in both sets
var inter = s1.Intersect(s2)       // HashSet(2, 3)

// Difference: elements in s1 but not s2
var diff = s1.Diff(s2)             // HashSet(1)

// Symmetric Difference: elements in either but not both
var symDiff = s1.SymmetricDiff(s2) // HashSet(1, 4)
```

### Set Operations (in-place modification)

```gala
var s1 = HashSetOf(1, 2, 3)
var s2 = HashSetOf(2, 3, 4)

// UnionInPlace: add all from s2 to s1
s1.UnionInPlace(s2)      // s1 is now {1, 2, 3, 4}

// IntersectInPlace: keep only elements also in s2
s1.IntersectInPlace(s2)  // s1 is now {2, 3, 4}

// DiffInPlace: remove elements that are in s2
s1.DiffInPlace(s2)       // s1 is now {}
```

### Set Relationships

```gala
var s1 = HashSetOf(1, 2)
var s2 = HashSetOf(1, 2, 3, 4)

s1.SubsetOf(s2)          // true: all elements of s1 are in s2
s2.SupersetOf(s1)        // true: s2 contains all of s1

var s3 = HashSetOf(5, 6)
s1.Disjoint(s3)          // true: no common elements
```

### Functional Operations

```gala
var s = HashSetOf(1, 2, 3, 4, 5, 6)

// Filter - returns new HashSet
var evens = s.Filter((x int) => x % 2 == 0)       // HashSet(2, 4, 6)
var odds = s.FilterNot((x int) => x % 2 == 0)     // HashSet(1, 3, 5)

// Partition - returns tuple of (matching, non-matching)
var parts = s.Partition((x int) => x % 2 == 0)
// parts.V1 = HashSet(2, 4, 6), parts.V2 = HashSet(1, 3, 5)

// Map - returns new HashSet
var doubled = s.Map[int]((x int) => x * 2)

// FoldLeft
var sum = s.FoldLeft[int](0, (acc int, x int) => acc + x)
```

### Predicates

```gala
var s = HashSetOf(1, 2, 3, 4, 5)

s.Exists((x int) => x > 4)    // true: at least one > 4
s.ForAll((x int) => x > 0)    // true: all are positive
s.Count((x int) => x % 2 == 0) // 2: count of even numbers
s.Find((x int) => x > 3)      // Some(4 or 5): first match (order not guaranteed)
```

### Element Access

```gala
var s = HashSetOf(1, 2, 3)

s.Head()                  // any element (order not guaranteed)
s.HeadOption()            // Some(element) or None for empty set
```

### Sorting

Returns `*Array[T]` since sets have no inherent order.

```gala
var s = HashSetOf(5, 3, 1)

s.Sorted()                                // Array(1, 3, 5)
s.SortWith((a, b) => a > b)               // Array(5, 3, 1)
s.SortBy((x) => x)                        // Array(1, 3, 5)
```

### Conversion

```gala
var s = HashSetOf(3, 1, 2)

s.ToSlice()               // []int (order not guaranteed)
s.ToArray()               // mutable Array
s.ToList()                // mutable List
s.Clone()                 // new HashSet with same elements
s.String()                // "HashSet(1, 2, 3)" (order may vary)
s.MkString(", ")          // "1, 2, 3" (order may vary)
```

---

## TreeSet[T]

A mutable sorted set implemented as a Red-Black tree. Maintains elements in sorted order and provides O(log n) operations.

### Type Requirements

Elements must be either:
- **Primitive types**: `int`, `string`, `float64`, etc. (compared using natural ordering)
- **Types implementing `Ordered[T]`**: Custom types that provide a `Compare(T) int` method

```gala
import . "martianoff/gala/std"

// Example custom type implementing Ordered
type Person struct {
    Name string
    Age  int
}

func (p Person) Compare(other Person) int {
    if p.Age < other.Age { return -1 }
    if p.Age > other.Age { return 1 }
    return 0
}
```

### Construction

```gala
// Empty tree set
var s = EmptyTreeSet[int]()

// From elements (sorted automatically)
var s = TreeSetOf(3, 1, 4, 1, 5)  // TreeSet(1, 3, 4, 5)

// From slice
var slice = SliceOf(3, 1, 2)
var s = TreeSetFromSlice(slice)   // TreeSet(1, 2, 3)
```

### Basic Operations

```gala
var s = TreeSetOf(5, 3, 7, 1, 9)

s.IsEmpty()      // false
s.NonEmpty()     // true
s.Size()         // 5
s.Length()       // 5 (alias)
```

### Membership and Modification - O(log n)

```gala
var s = TreeSetOf(1, 3, 5)

// Contains - O(log n)
s.Contains(3)           // true
s.Contains(4)           // false

// Add - O(log n)
s.Add(4)                // true, s is now TreeSet(1, 3, 4, 5)
s.Add(3)                // false (already present)

// Remove - O(log n)
s.Remove(3)             // true, s is now TreeSet(1, 4, 5)

// Clear
s.Clear()
```

### Min/Max Operations - O(log n)

```gala
var s = TreeSetOf(5, 3, 7, 1, 9)

// Get min/max
s.Min()                  // 1
s.Max()                  // 9
s.MinOption()            // Some(1)
s.MaxOption()            // Some(9)

// Pop (remove and return)
s.PopMin()               // 1, s is now TreeSet(3, 5, 7, 9)
s.PopMax()               // 9, s is now TreeSet(3, 5, 7)

// Head/Last (aliases)
s.Head()                 // same as Min()
s.Last()                 // same as Max()
```

### Range Queries (unique to TreeSet)

```gala
var s = TreeSetOf(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

// Range [from, to] inclusive - returns new TreeSet
var range1 = s.Range(3, 7)     // TreeSet(3, 4, 5, 6, 7)

// RangeFrom (>= value)
var range2 = s.RangeFrom(7)    // TreeSet(7, 8, 9, 10)

// RangeTo (<= value)
var range3 = s.RangeTo(4)      // TreeSet(1, 2, 3, 4)
```

### Set Operations (return new TreeSet)

```gala
var s1 = TreeSetOf(1, 2, 3)
var s2 = TreeSetOf(2, 3, 4)

// Union
var union = s1.Union(s2)         // TreeSet(1, 2, 3, 4)

// Intersection
var inter = s1.Intersect(s2)     // TreeSet(2, 3)

// Difference
var diff = s1.Diff(s2)           // TreeSet(1)
```

### Set Operations (in-place)

```gala
var s1 = TreeSetOf(1, 2, 3)
var s2 = TreeSetOf(2, 3, 4)

s1.UnionInPlace(s2)
s1.IntersectInPlace(s2)
s1.DiffInPlace(s2)
```

### Set Relationships

```gala
var s1 = TreeSetOf(1, 2)
var s2 = TreeSetOf(1, 2, 3, 4)

s1.SubsetOf(s2)          // true
s2.SupersetOf(s1)        // true
```

### Iteration (sorted order)

```gala
var s = TreeSetOf(3, 1, 4, 1, 5)

// Iterate in sorted order
s.ForEach((x int) => {
    fmt.Println(x)       // prints 1, 3, 4, 5
    return nil
})

// Iterate in reverse order
s.ForEachReverse((x int) => {
    fmt.Println(x)       // prints 5, 4, 3, 1
    return nil
})
```

### Functional Operations

```gala
var s = TreeSetOf(1, 2, 3, 4, 5, 6)

// Filter
var evens = s.Filter((x int) => x % 2 == 0)     // TreeSet(2, 4, 6)

// Partition
var parts = s.Partition((x int) => x % 2 == 0)
// parts.V1 = TreeSet(2, 4, 6), parts.V2 = TreeSet(1, 3, 5)

// Map - returns new TreeSet
var doubled = s.Map[int]((x int) => x * 2)

// FoldLeft (iterates in sorted order)
var sum = s.FoldLeft[int](0, (acc int, x int) => acc + x)

// Reduce (uses sorted order)
var sum2 = s.Reduce((a int, b int) => a + b)
```

### Predicates

```gala
var s = TreeSetOf(1, 2, 3, 4, 5)

s.Exists((x int) => x > 4)    // true
s.ForAll((x int) => x > 0)    // true
s.Count((x int) => x % 2 == 0) // 2
s.Find((x int) => x > 3)      // Some(4) - first in sorted order
```

### Sorting

Returns `*Array[T]`. `Sorted()` returns elements in natural order (already sorted by tree structure).

```gala
var s = TreeSetOf(30, 10, 20)

s.Sorted()                                // Array(10, 20, 30)
s.SortWith((a, b) => a > b)               // Array(30, 20, 10)
s.SortBy((x) => x)                        // Array(10, 20, 30)
```

### Conversion

```gala
var s = TreeSetOf(3, 1, 2)

s.ToSlice()               // []int{1, 2, 3} (sorted)
s.ToArray()               // mutable Array (sorted)
s.ToList()                // mutable List (sorted)
s.ToHashSet()             // HashSet (unordered, O(1) operations)
s.Clone()                 // new TreeSet with same elements
s.String()                // "TreeSet(1, 2, 3)"
s.MkString(", ")          // "1, 2, 3" (sorted)
```

---

## HashMap[K,V]

A mutable hash-based map providing O(1) average-case operations. See `collection_mutable/hashmap.gala` for the full API.

### Construction

```gala
var m = EmptyHashMap[string, int]()
var m = HashMapOf(("a", 1), ("b", 2), ("c", 3))
```

### Core Operations - O(1)

```gala
var m = HashMapOf(("a", 1), ("b", 2))

m.Put("c", 3)            // adds entry in-place
m.Get("a")               // Some(1)
m.GetOrElse("z", 0)      // 0
m.Contains("a")          // true
m.Remove("b")            // removes in-place
m.Size()                 // 2
m.Clear()                // removes all entries

// Functional operations
m.Filter((k, v) => v > 1)
m.MapValues[int]((v) => v * 2)
m.Sorted()               // Array of entries sorted by key
```

---

## TreeMap[K,V]

A mutable sorted map implemented as a Red-Black tree. Maintains entries in sorted key order and provides O(log n) operations with range queries.

**Type Requirements:** Keys must be `comparable` and either:
- A **primitive type** (int, string, float64, etc.) - compared automatically
- A **custom type** implementing the `Ordered[T]` interface

### Construction

```gala
// Empty map
var m = EmptyTreeMap[string, int]()

// From entries
var m = TreeMapOf(("cherry", 3), ("apple", 1), ("banana", 2))
// TreeMap(apple -> 1, banana -> 2, cherry -> 3)

// From Go map
var goMap = map[string]int{"x": 10, "y": 20}
var m2 = TreeMapFromGoMap(goMap)
```

### Core Operations - O(log n)

```gala
var m = EmptyTreeMap[string, int]()

// Put (in-place, returns true if new key)
m.Put("a", 1)            // true
m.Put("a", 10)           // false (updated existing)

// Get
m.Get("a")               // Some(10)
m.GetOrElse("z", 0)      // 0
m.Contains("a")          // true

// Remove (in-place)
m.Remove("a")

// Size
m.Size()                 // 0
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

// PutIfAbsent - only inserts if key doesn't exist
m.PutIfAbsent("a", 99)   // no change, "a" already exists
m.PutIfAbsent("c", 3)    // inserts "c" -> 3

// PopMinEntry / PopMaxEntry
val min = m.PopMinEntry() // removes and returns smallest entry
val max = m.PopMaxEntry() // removes and returns largest entry

// Clear
m.Clear()

// Clone
var m2 = m.Clone()       // independent copy

// PutFrom - merge from another TreeMap
m.PutFrom(otherTreeMap)

// Merge with combiner
m.Merge(other, (v1, v2) => v1 + v2)
```

### Functional Operations

```gala
var m = TreeMapOf(("a", 1), ("b", 2), ("c", 3), ("d", 4))

// Filter (returns new TreeMap)
m.Filter((k, v) => v % 2 == 0)      // TreeMap(b -> 2, d -> 4)

// MapValues (returns new TreeMap)
m.MapValues[int]((v) => v * 10)     // TreeMap(a -> 10, b -> 20, ...)

// FoldLeftKV
m.FoldLeftKV[int](0, (acc, k, v) => acc + v)  // 10

// FilterInPlace (modifies in-place)
m.FilterInPlace((k, v) => v > 2)

// Iteration (sorted key order)
m.ForEachKV((k, v) => { fmt.Printf("%s=%d ", k, v) })
```

### Sorted API

```gala
var m = TreeMapOf(("c", 30), ("a", 10), ("b", 20))

m.Sorted()                              // Array((a,10), (b,20), (c,30))
m.SortWith((a, b) => a.V2 < b.V2)      // Array((a,10), (b,20), (c,30))
m.SortBy[int]((e) => -e.V2)            // Array((c,30), (b,20), (a,10))
```

### Conversion

```gala
var m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

m.Keys()                 // TreeSet("a", "b", "c")
m.Values()               // [1, 2, 3]
m.ToArray()              // Array of Tuple entries
m.ToGoMap()              // map[string]int
m.ToHashMap()            // mutable HashMap
m.String()               // "TreeMap(a -> 1, b -> 2, c -> 3)"
m.MkString(", ")         // "(a, 1), (b, 2), (c, 3)"
```

---

## Choosing Between HashMap and TreeMap

| Use Case | Recommended | Reason |
|----------|-------------|--------|
| Fastest put/get/remove | HashMap | O(1) vs O(log n) |
| Sorted key iteration | TreeMap | Maintains order |
| Min/Max key queries | TreeMap | O(log n) vs O(n) |
| Range queries on keys | TreeMap | Only TreeMap supports |
| Key order doesn't matter | HashMap | Faster on average |

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

### HashSet Advantages
- O(1) average-case for add/remove/contains
- Faster set operations (union, intersect, diff)
- Lower constant factors for large sets

### TreeSet Advantages
- Sorted iteration
- O(log n) min/max access
- Range queries (Range, RangeFrom, RangeTo)
- Consistent O(log n) worst-case (no hash collisions)

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
    var single = ArrayOf(42)
    var multi = ArrayOf(1, 2, 3)

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
| Creation | 36,910 | 29,562 | ~same |
| CreationWithCap | 10,967 | 7,724 | ~same |
| Append | 1 | 1 | Both O(1) amortized |
| Prepend | 1,028 | 6,701 | **GALA 6.5x faster** |
| Get(5000) | 1 | 0 | Both O(1) |
| Set(5000) | 1 | 1 | Both O(1) |
| Filter | 20,611 | 14,536 | ~same |
| Map | 8,734 | 7,500 | ~same |
| FoldLeft | 951 | 972 | ~same |
| Clone | 9,001 | 7,198 | ~same |
| Reverse | 2,816 | 2,067 | ~same |

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

### Set Results (ns/op) - 10,000 Elements

| Operation | HashSet | TreeSet |
|-----------|--------:|--------:|
| Creation | 474,645 | 6,652,263 |
| Add | 52 | 1,478 |
| Contains (hit) | 17 | 442 |
| Contains (miss) | 18 | 711 |
| Remove | 51 | 1,449 |
| Min | O(n) | 5 |
| Max | O(n) | 8 |
| Filter | 311,500 | 3,207,835 |
| Union (5k+5k) | 600,776 | 6,120,501 |
| Intersect (5k+5k) | 273,991 | 3,330,017 |
| Range | - | 3,467,559 |
| Clone | 559,014 | 7,057,953 |

### Set Scaling Results

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| HashSet.Creation | 3,165 ns | 474,645 ns | 6,934,149 ns |
| TreeSet.Creation | 18,644 ns | 6,652,263 ns | 91,282,413 ns |

### Map Results (ns/op) - 10,000 Elements

| Operation | HashMap | TreeMap |
|-----------|--------:|--------:|
| Creation | 585,567 | 5,824,254 |
| Put | 59 | 956 |
| Get (hit) | 16 | 296 |
| Get (miss) | 18 | 462 |
| Contains (hit) | 17 | 294 |
| Contains (miss) | 16 | 463 |
| Remove | 63 | 990 |
| MinKey | O(n) | 5 |
| MaxKey | O(n) | 8 |
| Filter | 411,615 | 2,264,105 |
| MapValues | 808,557 | 4,949,690 |
| Range | - | 2,425,290 |
| Clone | 789,238 | 4,880,913 |

### Map Scaling Results

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| HashMap.Creation | 4,996 ns | 585,567 ns | 9,712,457 ns |
| TreeMap.Creation | 11,441 ns | 5,824,254 ns | 74,893,150 ns |

### When to Use Each Map Type

| Scenario | Recommendation | Reason |
|----------|----------------|--------|
| Fast key-value lookup | HashMap | 16 ns vs 296 ns |
| Sorted key iteration | TreeMap | Only TreeMap maintains key order |
| Min/Max key queries | TreeMap | 5 ns vs O(n) scan |
| Range queries on keys | TreeMap | Only TreeMap supports |
| Bulk operations | HashMap | 2-6x faster for filter/map |
| Consistent performance | TreeMap | No hash collision risk |

### When to Use Each Set Type

| Scenario | Recommendation | Reason |
|----------|----------------|--------|
| Fast membership testing | HashSet | 17 ns vs 442 ns |
| Sorted iteration | TreeSet | Only TreeSet maintains order |
| Min/Max queries | TreeSet | 5 ns vs O(n) scan |
| Range queries | TreeSet | Only TreeSet supports |
| Large set operations | HashSet | 2-10x faster for union/intersect |
| Consistent performance | TreeSet | No hash collision risk |

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
    var numbers = ArrayOf(-5, -2, 0, 1, 2, 3, 4, 5)

    // Group by category using pattern matching
    var groups = numbers.GroupBy[string](categorize)

    // Print each group
    for category, arr := range groups {
        fmt.Printf("%s: %s\n", category, arr.String())
    }
}
```

## Example: Using HashSet for Deduplication

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
)

func main() {
    // Find common elements between two datasets
    var dataset1 = SliceOf(1, 2, 3, 4, 5, 6, 7, 8)
    var dataset2 = SliceOf(5, 6, 7, 8, 9, 10, 11, 12)

    var set1 = HashSetFromSlice(dataset1)
    var set2 = HashSetFromSlice(dataset2)

    // Find intersection (O(min(n,m)) average)
    var common = set1.Intersect(set2)
    fmt.Printf("Common elements: %s\n", common.String())  // HashSet(5, 6, 7, 8)

    // Find elements only in first dataset
    var onlyInFirst = set1.Diff(set2)
    fmt.Printf("Only in first: %s\n", onlyInFirst.String())  // HashSet(1, 2, 3, 4)

    // Check membership (O(1) average)
    fmt.Printf("Contains 5: %t\n", set1.Contains(5))  // true
}
```

## Example: Using TreeSet as Priority Queue

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
)

func main() {
    // Process tasks by priority (lower number = higher priority)
    var tasks = EmptyTreeSet[int]()

    // Add tasks with priorities
    tasks.Add(5)   // low priority
    tasks.Add(1)   // highest priority
    tasks.Add(3)   // medium priority
    tasks.Add(2)   // high priority

    // Process in priority order
    for tasks.NonEmpty() {
        var priority = tasks.PopMin()  // O(log n)
        fmt.Printf("Processing task with priority %d\n", priority)
    }
    // Output: 1, 2, 3, 5
}
```

## Example: TreeSet Range Queries

```gala
package main

import (
    "fmt"
    . "martianoff/gala/collection_mutable"
)

func main() {
    // Find students with grades in specific ranges
    var grades = TreeSetOf(45, 55, 65, 72, 78, 85, 92, 98)

    // Find passing grades (60+)
    var passing = grades.RangeFrom(60)
    fmt.Printf("Passing: %s\n", passing.String())  // TreeSet(65, 72, 78, 85, 92, 98)

    // Find B grades (80-89)
    var bGrades = grades.Range(80, 89)
    fmt.Printf("B grades: %s\n", bGrades.String())  // TreeSet(85)

    // Find failing grades (<60)
    var failing = grades.RangeTo(59)
    fmt.Printf("Failing: %s\n", failing.String())  // TreeSet(45, 55)
}
```
