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
- O(eC) - Effectively constant (O(log32 n) ≈ 7 operations for 1 billion elements)

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
list.TakeWhile((x int) => x < 4)  // List(1, 2, 3)
list.DropWhile((x int) => x < 3)  // List(3, 4, 5)
list.SplitAt(2)              // Tuple(List(1, 2), List(3, 4, 5))
```

### Searching

```gala
val list = ListOf(1, 2, 3, 4, 5)

list.Contains(3)             // true
list.IndexOf(3)              // 2
list.IndexOf(10)             // -1
list.Find((x int) => x > 3)  // Some(4)
```

### Transformations

```gala
val list = ListOf(1, 2, 3)

// Map
list.Map[int]((x int) => x * 2)  // List(2, 4, 6)

// FlatMap
list.FlatMap[int]((x int) => ListOf(x, x * 10))
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
ListOf(1, 2, 2, 3, 1).Distinct()  // List(1, 2, 3)
```

### Folding and Reduction

```gala
val list = ListOf(1, 2, 3, 4)

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
val list = ListOf(2, 4, 6, 8)

list.Exists((x int) => x == 4)  // true
list.ForAll((x int) => x % 2 == 0)  // true
list.Count((x int) => x > 4)  // 2
```

### Zipping

```gala
val nums = ListOf(1, 2, 3)
val strs = ListOf("a", "b", "c")

nums.Zip[string](strs)
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
val arr = ArrayOf(1, 2, 3, 4, 5)

// From slice
val slice = SliceOf(1, 2, 3)
val arr2 = ArrayFrom(slice)
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
arr.TakeWhile((x int) => x < 4)   // Array(1, 2, 3)
arr.DropWhile((x int) => x < 3)   // Array(3, 4, 5)
arr.SplitAt(2)               // Tuple(Array(1, 2), Array(3, 4, 5))
```

### Searching

```gala
val arr = ArrayOf(1, 2, 3, 2, 1)

arr.Contains(3)              // true
arr.IndexOf(2)               // 1
arr.LastIndexOf(2)           // 3
arr.Find((x int) => x > 2)   // Some(3)
arr.FindLast((x int) => x < 3)  // Some(1)
```

### Transformations

```gala
val arr = ArrayOf(1, 2, 3)

// Map
arr.Map[int]((x int) => x * 2)  // Array(2, 4, 6)

// FlatMap
arr.FlatMap[int]((x int) => ArrayOf(x, x * 10))
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
ArrayOf(1, 2, 2, 3, 1).Distinct()  // Array(1, 2, 3)
```

### Folding and Reduction

```gala
val arr = ArrayOf(1, 2, 3, 4)

arr.FoldLeft[int](0, (acc int, x int) => acc + x)  // 10
arr.FoldRight[int](0, (x int, acc int) => x + acc)  // 10
arr.Reduce((a int, b int) => a + b)  // 10
arr.ReduceOption((a int, b int) => a + b)  // Some(10)
```

### Predicates

```gala
val arr = ArrayOf(2, 4, 6, 8)

arr.Exists((x int) => x == 4)  // true
arr.ForAll((x int) => x % 2 == 0)  // true
arr.Count((x int) => x > 4)  // 2
```

### Zipping

```gala
val nums = ArrayOf(1, 2, 3)
val strs = ArrayOf("a", "b", "c")

nums.Zip[string](strs)
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
arr.ForEach((x int) => {
    fmt.Println(x)
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

### Basic Operations

```gala
val set = HashSetOf(1, 2, 3, 4, 5)

set.IsEmpty()      // false
set.NonEmpty()     // true
set.Size()         // 5
set.Length()       // 5 (alias for Size)
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

### Transformations

```gala
val set = HashSetOf(1, 2, 3, 4, 5)

// Filter
set.Filter((x int) => x % 2 == 0)     // HashSet(2, 4)
set.FilterNot((x int) => x % 2 == 0)  // HashSet(1, 3, 5)

// Partition
set.Partition((x int) => x > 3)
// Tuple(HashSet(4, 5), HashSet(1, 2, 3))

// Map (use standalone function)
MapHashSet[int, int](set, (x int) => x * 2)  // HashSet(2, 4, 6, 8, 10)
```

### Folding and Reduction

```gala
val set = HashSetOf(1, 2, 3, 4, 5)

// FoldLeft
set.FoldLeft[int](0, (acc int, x int) => acc + x)  // 15

// Reduce
set.Reduce((a int, b int) => a + b)  // 15

// ReduceOption (safe for empty sets)
set.ReduceOption((a int, b int) => a + b)  // Some(15)
```

### Predicates

```gala
val set = HashSetOf(2, 4, 6, 8)

set.Exists((x int) => x > 5)           // true
set.ForAll((x int) => x % 2 == 0)      // true
set.Count((x int) => x > 4)            // 2
set.Find((x int) => x > 5)             // Some(6) or Some(8)
```

### Sorting

Returns `Array[T]` since sets have no inherent order.

```gala
val set = HashSetOf(5, 3, 1)

set.Sorted()                                // Array(1, 3, 5)
set.SortWith((a, b) => a > b)               // Array(5, 3, 1)
set.SortBy((x) => x)                        // Array(1, 3, 5)
```

### Conversion

```gala
val set = HashSetOf(1, 2, 3)

set.ToSlice()       // []int{1, 2, 3} (order not guaranteed)
set.ToList()        // List(1, 2, 3) (order not guaranteed)
set.ToArray()       // Array(1, 2, 3) (order not guaranteed)
set.String()        // "HashSet(1, 2, 3)"
set.MkString(", ")  // "1, 2, 3" (order not guaranteed)
```

### ForEach (Side Effects)

```gala
set.ForEach((x int) => {
    fmt.Println(x)
})
```

### Pattern Matching

```gala
val set = HashSetOf(1, 2, 3)

val result = set match {
    case s: HashSet[_] if s.IsEmpty() => "empty"
    case s: HashSet[_] => fmt.Sprintf("has %d elements", s.Size())
    case _ => "unknown"
}
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

// Example: Custom type implementing Ordered
type Person struct {
    Name string
    Age  int
}

func (p Person) Compare(other Person) int {
    if p.Age < other.Age { return -1 }
    if p.Age > other.Age { return 1 }
    return 0
}

// Now Person can be used in TreeSet (sorted by age)
val people = TreeSetOf(
    Person(Name = "Alice", Age = 30),
    Person(Name = "Bob", Age = 25),
)
// people.Min() returns Person("Bob", 25)
```

### Construction

```gala
// Empty set
val empty = EmptyTreeSet[int]()

// From elements
val set = TreeSetOf(1, 2, 3, 4, 5)

// From slice
val slice = SliceOf(3, 1, 4, 1, 5)
val set2 = TreeSetFromSlice(slice)  // TreeSet(1, 3, 4, 5) - sorted, no duplicates
```

### Basic Operations

```gala
val set = TreeSetOf(5, 3, 1, 4, 2)

set.IsEmpty()      // false
set.NonEmpty()     // true
set.Size()         // 5
set.Length()       // 5 (alias for Size)
```

### Element Operations - O(log n)

```gala
val set = TreeSetOf(1, 2, 3)

// Add element (returns new set)
set.Add(4)               // TreeSet(1, 2, 3, 4)

// Remove element (returns new set)
set.Remove(2)            // TreeSet(1, 3)

// Check membership
set.Contains(2)          // true
set.Contains(10)         // false
```

### Min/Max Operations - O(log n)

TreeSet's main advantage over HashSet: efficient min/max access.

```gala
val set = TreeSetOf(5, 3, 1, 4, 2)

// Min - smallest element
set.Min()                // 1
set.MinOption()          // Some(1)

// Max - largest element
set.Max()                // 5
set.MaxOption()          // Some(5)

// Head/Last (aliases for Min/Max)
set.Head()               // 1 (same as Min)
set.Last()               // 5 (same as Max)
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

### Set Operations

```gala
val a = TreeSetOf(1, 2, 3, 4)
val b = TreeSetOf(3, 4, 5, 6)

// Union - all elements from both sets
a.Union(b)               // TreeSet(1, 2, 3, 4, 5, 6)

// Intersection - elements in both sets
a.Intersect(b)           // TreeSet(3, 4)

// Difference - elements in a but not in b
a.Diff(b)                // TreeSet(1, 2)

// Subset check
TreeSetOf(1, 2).SubsetOf(a)  // true
```

### Transformations

```gala
val set = TreeSetOf(1, 2, 3, 4, 5)

// Filter
set.Filter((x int) => x % 2 == 0)     // TreeSet(2, 4)
set.FilterNot((x int) => x % 2 == 0)  // TreeSet(1, 3, 5)

// Partition
set.Partition((x int) => x > 3)
// Tuple(TreeSet(4, 5), TreeSet(1, 2, 3))

// Map (use standalone function)
MapTreeSet(set, (x int) => x * 2)  // TreeSet(2, 4, 6, 8, 10)
```

### Folding and Reduction

```gala
val set = TreeSetOf(1, 2, 3, 4, 5)

// FoldLeft - processes elements in sorted order!
set.FoldLeft[int](0, (acc int, x int) => acc + x)  // 15

// Reduce
set.Reduce((a int, b int) => a + b)  // 15

// ReduceOption (safe for empty sets)
set.ReduceOption((a int, b int) => a + b)  // Some(15)
```

### Predicates

```gala
val set = TreeSetOf(2, 4, 6, 8)

set.Exists((x int) => x > 5)           // true
set.ForAll((x int) => x % 2 == 0)      // true
set.Count((x int) => x > 4)            // 2
set.Find((x int) => x > 5)             // Some(6) - first in sorted order
```

### Sorting

Returns `Array[T]`. `Sorted()` returns elements in natural order (already sorted by tree structure).

```gala
val set = TreeSetOf(30, 10, 20)

set.Sorted()                                // Array(10, 20, 30)
set.SortWith((a, b) => a > b)               // Array(30, 20, 10)
set.SortBy((x) => x)                        // Array(10, 20, 30)
```

### Conversion

```gala
val set = TreeSetOf(3, 1, 2)

set.ToSlice()       // []int{1, 2, 3} - sorted order
set.ToList()        // List(1, 2, 3) - sorted order
set.ToArray()       // Array(1, 2, 3) - sorted order
set.ToHashSet()     // HashSet(1, 2, 3) - loses order, gains O(eC) lookup
set.String()        // "TreeSet(1, 2, 3)"
set.MkString(", ")  // "1, 2, 3" - sorted order
```

### ForEach (Side Effects) - Sorted Order

```gala
// Elements processed in sorted order
set.ForEach((x int) => {
    fmt.Println(x)  // Prints: 1, 2, 3 (in order)
})
```

### Pattern Matching

```gala
val set = TreeSetOf(1, 2, 3)

val result = set match {
    case s: TreeSet[_] if s.IsEmpty() => "empty"
    case s: TreeSet[_] => fmt.Sprintf("has %d elements, min=%v", s.Size(), s.Min())
    case _ => "unknown"
}
```

---

## HashMap[K,V]

An immutable hash-based map with effectively constant time operations. See `collection_immutable/hashmap.gala` for the full API.

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
m.MapValues[int]((v) => v * 2)               // HashMap(a -> 2, b -> 4, c -> 6)
m.FoldLeftKV[int](0, (acc, k, v) => acc + v) // 6
m.Sorted()                                    // Array((a,1), (b,2), (c,3))
```

---

## TreeMap[K,V]

An immutable sorted map implemented as a Red-Black tree. Maintains entries in sorted key order and provides O(log n) operations with range queries.

**Type Requirements:** Keys must be `comparable` and either:
- A **primitive type** (int, string, float64, etc.) - compared automatically
- A **custom type** implementing the `Ordered[T]` interface

### Construction

```gala
// Empty map
val empty = EmptyTreeMap[string, int]()

// From entries
val m = TreeMapOf(("cherry", 3), ("apple", 1), ("banana", 2))
// TreeMap(apple -> 1, banana -> 2, cherry -> 3)  -- sorted by key

// From slice of tuples
val m2 = TreeMapFromSlice(SliceOf(("a", 1), ("b", 2)))

// From Go map
var goMap = map[string]int{"x": 10, "y": 20}
val m3 = TreeMapFromGoMap(goMap)
```

### Core Operations - O(log n)

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

// Put (returns new map)
val m2 = m.Put("d", 4)

// Get
m.Get("b")              // Some(2)
m.Get("z")              // None
m.GetOrElse("z", 0)     // 0
m.Apply("a")            // 1 (panics if missing)

// Contains
m.Contains("a")         // true

// Remove (returns new map)
val m3 = m.Remove("b")  // TreeMap(a -> 1, c -> 3)

// Size
m.Size()                // 3
```

### Min/Max Operations

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

m.MinKey()              // "a"
m.MaxKey()              // "c"
m.MinEntry()            // Tuple("a", 1)
m.MaxEntry()            // Tuple("c", 3)
m.MinKeyOption()        // Some("a")
m.MaxKeyOption()        // Some("c")
```

### Range Queries

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3), ("d", 4), ("e", 5))

// Range [from, to] inclusive
m.Range("b", "d")       // TreeMap(b -> 2, c -> 3, d -> 4)

// RangeFrom (>= key)
m.RangeFrom("c")        // TreeMap(c -> 3, d -> 4, e -> 5)

// RangeTo (<= key)
m.RangeTo("c")          // TreeMap(a -> 1, b -> 2, c -> 3)
```

### Functional Operations

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3), ("d", 4))

// Filter
m.Filter((k, v) => v % 2 == 0)     // TreeMap(b -> 2, d -> 4)
m.FilterKeys((k) => k > "b")       // TreeMap(c -> 3, d -> 4)
m.FilterValues((v) => v > 2)       // TreeMap(c -> 3, d -> 4)

// MapValues
m.MapValues[int]((v) => v * 10)    // TreeMap(a -> 10, b -> 20, c -> 30, d -> 40)

// FoldLeftKV
m.FoldLeftKV[int](0, (acc, k, v) => acc + v)  // 10

// Merge with combiner
val m2 = TreeMapOf(("c", 30), ("e", 5))
m.Merge(m2, (v1, v2) => v1 + v2)
// TreeMap(a -> 1, b -> 2, c -> 33, d -> 4, e -> 5)

// Iteration (sorted key order)
m.ForEachKV((k, v) => { fmt.Printf("%s=%d ", k, v) })
// a=1 b=2 c=3 d=4
```

### Sorted API

```gala
val m = TreeMapOf(("c", 30), ("a", 10), ("b", 20))

// Sorted by key (no-op, already sorted)
m.Sorted()                              // Array((a,10), (b,20), (c,30))

// Custom ordering (sort by value)
m.SortWith((a, b) => a.V2 < b.V2)      // Array((a,10), (b,20), (c,30))

// Sort by extracted key (descending by value)
m.SortBy[int]((e) => -e.V2)            // Array((c,30), (b,20), (a,10))
```

### Conversion

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3))

m.Keys()                // TreeSet("a", "b", "c")
m.Values()              // [1, 2, 3] (sorted key order)
m.ToArray()             // Array(("a",1), ("b",2), ("c",3))
m.ToGoMap()             // map[string]int{"a":1, "b":2, "c":3}
m.ToHashMap()           // HashMap(a -> 1, b -> 2, c -> 3)
m.String()              // "TreeMap(a -> 1, b -> 2, c -> 3)"
m.MkString(", ")        // "(a, 1), (b, 2), (c, 3)"
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

**Note:** With the prefix buffer optimization, Array now has O(1) amortized prepend, making it competitive with List for prepend-heavy workloads. Choose List when you need true O(1) without amortization, or Array when you also need random access.

### Choosing Between HashMap and TreeMap

| Need | Choose |
|------|--------|
| Fastest put/get/remove | HashMap (O(eC) vs O(log n)) |
| Entries in sorted key order | TreeMap |
| Min/Max key access | TreeMap (O(log n) vs O(n)) |
| Range queries on keys | TreeMap |
| Key order doesn't matter | HashMap |

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

### HashSet Advantages
- O(eC) add, remove, and contains operations (faster than TreeSet)
- Efficient set operations (union, intersection, difference)
- No duplicate elements
- Works with any `comparable` type
- Best choice when order doesn't matter

### TreeSet Advantages
- O(log n) add, remove, and contains operations
- Elements maintained in sorted order
- O(log n) min/max access
- Range queries (elements in [from, to])
- Sorted iteration guaranteed
- Best choice when you need ordering or range operations

---

## Implementation Details

### List
List is implemented as a classic persistent linked list (cons list). Each node contains a value and a pointer to the tail. This provides:
- Structural sharing: prepending creates a new node pointing to the existing list
- Cached length for O(1) size queries

### Array
Array is implemented as a 32-way branching trie (similar to Scala's Vector and Clojure's PersistentVector) with several Scala-inspired optimizations:
- Tree depth of at most 7 for up to 34 billion elements
- Path copying for updates, sharing unaffected subtrees
- Effectively constant time operations (O(log32 n))
- **Prefix buffer**: prepended elements are stored in a separate buffer until it reaches 32 elements, then consolidated (O(1) amortized prepend)

### HashSet
HashSet is implemented as a Hash Array Mapped Trie (HAMT), similar to Scala's HashSet:
- 32-way branching trie with bitmap indexing
- Hash values determine path through the trie
- Collision handling at leaf nodes (when max depth reached)
- Path copying for updates, sharing unaffected subtrees
- Effectively constant time operations (O(log32 n))

### TreeSet
TreeSet is implemented as a persistent Red-Black tree, similar to Scala's TreeSet:
- Self-balancing binary search tree with red/black coloring
- Height guaranteed to be at most 2*log(n+1)
- Path copying for updates, sharing unaffected subtrees
- O(log n) operations with in-order traversal for sorted iteration
- Supports range queries by exploiting tree structure

### HashMap
HashMap is implemented as a Hash Array Mapped Trie (HAMT), similar to Scala's HashMap:
- Same HAMT structure as HashSet but stores key-value pairs
- Effectively constant time operations (O(log32 n))
- Path copying for updates

### TreeMap
TreeMap is implemented as a persistent Red-Black tree, similar to Scala's TreeMap:
- Same Okasaki-style functional Red-Black tree as TreeSet but stores key-value pairs
- O(log n) operations with in-order traversal for sorted key iteration
- Range queries by exploiting tree structure (Range, RangeFrom, RangeTo)
- Keys() returns a TreeSet for efficient sorted key access

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

### Sequence Results (ns/op) - 10,000 Elements

| Operation | GALA List | GALA Array | Go Slice (immutable) |
|-----------|----------:|-----------:|---------------------:|
| Creation | 129,766 |  3,670,004 | 34,890,000 |
| Prepend | 0 |          0 | 5,229 |
| Append | - |        445 | 7,443 |
| Head | 1 |          5 | 1 |
| Get(5000) | 4,088 |          5 | 0 |
| Update(5000) | - |        544 | 7,337 |
| Filter | 169,000 |    78,000 | 15,463 |
| Map | 265,000 |    114,000 | 10,476 |
| FoldLeft | 9,527 |     41,000 | 1,039 |
| Take(5000) | - |     54,000 | 515 |
| Drop(5000) | - |     52,000 | 1,044 |

### Set Results (ns/op) - 10,000 Elements

| Operation | HashSet | TreeSet |
|-----------|--------:|--------:|
| Creation | 6,860,911 | 11,040,081 |
| Add | 1,078 | 1,218 |
| Contains (hit) | 25 | 707 |
| Contains (miss) | 44 | 952 |
| Remove | 1,430 | 1,932 |
| Min | O(n) | 17 |
| Max | O(n) | 27 |
| Filter | 6,470,311 | 12,195,500 |

### Set Operations (ns/op) - 1,000 Elements Each

| Operation | HashSet | TreeSet |
|-----------|--------:|--------:|
| Union | 678,951 | 1,353,863 |
| Intersect | 500,559 | 1,196,271 |
| Range | O(n) | 12,695,384 |

### Scaling Results - Sequences

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| List.Creation | 2,067 ns | 129,766 ns | 1,239,000 ns |
| Array.Creation | 17,011 ns | 3,670,004 ns | 52,193,000 ns |

### Scaling Results - Sets

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| HashSet.Creation | 34,120 ns | 6,860,911 ns | 174,706,970 ns |
| TreeSet.Creation | 81,696 ns | 11,040,081 ns | 345,415,150 ns |

### Map Results (ns/op) - 10,000 Elements

| Operation | HashMap | TreeMap |
|-----------|--------:|--------:|
| Creation | 8,224,307 | 12,037,826 |
| Put | 1,170 | 1,767 |
| Get (hit) | 25 | 368 |
| Get (miss) | 27 | 470 |
| Contains (hit) | 35 | 368 |
| Contains (miss) | 22 | 480 |
| Remove | 816 | 1,215 |
| MinKey | O(n) | 15 |
| MaxKey | O(n) | 19 |
| Filter | 3,678,677 | 7,988,481 |
| MapValues | 7,174,073 | 17,429,089 |
| PutAll (2x1000) | 754,050 | 1,092,953 |

### Scaling Results - Maps

| Operation | 100 elements | 10,000 elements | 100,000 elements |
|-----------|-------------:|----------------:|-----------------:|
| HashMap.Creation | 35,016 ns | 8,224,307 ns | 112,375,790 ns |
| TreeMap.Creation | 30,668 ns | 12,037,826 ns | 202,606,830 ns |

**Notes:**
- GALA List uses Prepend (O(1)), GALA Array uses Append (O(eC)), GALA HashSet uses Add (O(eC))
- Go Slice (immutable): copy-on-write style, full copy on each modification
- `-` indicates operation not measured or not the primary use case
- Array uses optimized bulk building for Filter, Map, Take, Drop operations
- HashSet/HashMap Contains is O(eC) regardless of collection size

### Key Performance Insights

**List Strengths:**
- **O(1) Prepend** (1 ns): Fastest way to build collections from the front
- **O(1) Head/Tail**: Efficient for stack-like patterns
- **Linear Creation**: Scales well (10x elements ≈ 10x time)

**Array Strengths:**
- **O(eC) Prepend** (0 ns): Amortized constant time using prefix buffer (Scala-inspired)
- **O(eC) Append** (460 ns): 16x faster than immutable slice copy
- **O(eC) Get** (5 ns): Constant random access regardless of position
- **O(eC) Update** (544 ns): 14x faster than immutable slice copy

**HashSet Strengths:**
- **O(eC) Contains** (25 ns): Fast membership testing regardless of set size
- **O(eC) Add** (1,078 ns): Efficient element insertion
- **O(eC) Remove** (1,430 ns): Efficient element removal
- **Set operations**: Union, intersection, difference in O(m) time (m = smaller set)

**TreeSet Strengths:**
- **Sorted order**: Elements always maintained in sorted order
- **O(log n) Min/Max** (17-27 ns): Instant access to smallest/largest elements
- **Range queries**: Efficient retrieval of elements in a range
- **Sorted iteration**: ForEach, ToSlice, etc. all produce sorted output

**HashMap Strengths:**
- **O(eC) Get** (25 ns): Fast key-value lookup regardless of map size
- **O(eC) Put** (1,170 ns): Efficient insertion
- **O(eC) Remove** (816 ns): Efficient removal
- **Fastest map operations**: ~15x faster Get than TreeMap

**TreeMap Strengths:**
- **Sorted key order**: Entries maintained in sorted key order
- **O(log n) MinKey/MaxKey** (15-19 ns): Instant access to smallest/largest keys
- **Range queries**: Efficient retrieval of entries in a key range
- **Sorted iteration**: ForEachKV, ToArray, etc. all produce sorted output

**When to Choose HashMap vs TreeMap:**

| Need | Choose |
|------|--------|
| Fastest get/put/remove | HashMap (O(eC) vs O(log n)) |
| Entries in sorted key order | TreeMap |
| Min/Max key access | TreeMap (O(log n) vs O(n)) |
| Range queries on keys | TreeMap |
| Key order doesn't matter | HashMap |

**When to Choose HashSet vs TreeSet:**

| Need | Choose |
|------|--------|
| Fastest contains/add/remove | HashSet (O(eC) vs O(log n)) |
| Elements in sorted order | TreeSet |
| Min/Max of set | TreeSet (O(log n) vs O(n)) |
| Range queries | TreeSet |
| Order doesn't matter | HashSet |

**Comparison to Go Immutable Slices:**

| Operation | GALA Array | Go Slice (copy) | GALA Advantage |
|-----------|----------:|----------------:|----------------|
| Creation(10k) | 3.7ms | 34.9ms | **9x faster** |
| Prepend | 0 ns | 5,229 ns | **∞ faster** (O(1) vs O(n)) |
| Append | 445 ns | 7,443 ns | **17x faster** |
| Update | 544 ns | 7,337 ns | **13x faster** |
| Get | 5 ns | 0 ns | ~same |

**When to Use Each:**

| Scenario | Recommendation |
|----------|----------------|
| Stack operations (LIFO) | List |
| Queue-like building (append) | Array |
| Random access needed | Array |
| Frequent updates | Array |
| Large collections with sharing | Array |
| Recursive algorithms | List |
| Fast membership testing | HashSet |
| Set operations (union, intersect) | HashSet (fastest) or TreeSet (sorted) |
| Unique elements only | HashSet or TreeSet |
| Need sorted set | TreeSet |
| Need min/max of set | TreeSet |
| Need range queries | TreeSet or TreeMap |
| Fast key-value lookup | HashMap |
| Sorted key-value map | TreeMap |
| Min/max key of map | TreeMap |
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
    val numbers = ListOf(1, 2, 3, 4, 5)

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
