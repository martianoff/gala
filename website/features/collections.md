---
layout: default
title: "Golang Immutable Collections — List, Array, HashMap, TreeMap for Go"
description: "GALA provides immutable functional collections for Go — List, Array, HashMap, HashSet, TreeSet, TreeMap with Map, Filter, FoldLeft, Collect, SortBy, and lambda type inference. Beyond what samber/lo offers."
keywords: "golang immutable collections, go immutable list, go immutable hashmap, golang functional collections, go map filter reduce, golang persistent data structures, go functional programming collections, golang treemap, gala collections"
permalink: /features/collections/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/collections/">Features</a> / Functional Collections</p>

# Golang Immutable Collections — Functional Data Structures for Go

Libraries like [samber/lo](https://github.com/samber/lo) bring Map/Filter/Reduce to Go slices, but they operate on mutable data and lack persistent data structures. [benbjohnson/immutable](https://github.com/benbjohnson/immutable) provides immutable collections, but without the rich functional API. GALA combines both: a full suite of **immutable, functional collection types** — List, Array, HashMap, HashSet, TreeSet, TreeMap — with Scala-level operations, all compiling to native Go.

Every collection is persistent: operations return new collections, leaving the original untouched. Structural sharing keeps allocations efficient. Lambda parameter types are [inferred automatically](/features/type-inference/), so pipelines read cleanly without type annotations.

```gala
import . "martianoff/gala/collection_immutable"

val nums = ArrayOf(1, 2, 3, 4, 5)
val result = nums
    .Filter((x) => x % 2 == 0)
    .Map((x) => x * 10)
    .FoldLeft(0, (acc, x) => acc + x)
Println(result)  // 60
```

---

## Collection Types at a Glance

| Collection | Best For | Lookup | Insert | Ordered |
|------------|----------|--------|--------|---------|
| **List[T]** | Stack operations, recursive algorithms | O(n) | O(1) prepend | Insertion order |
| **Array[T]** | Random access, append-heavy workloads | O(eC) | O(eC) append, O(1)* prepend | Insertion order |
| **HashMap[K,V]** | Fast key-value lookup | O(eC) | O(eC) | Unordered |
| **HashSet[T]** | Fast membership testing, set operations | O(eC) | O(eC) | Unordered |
| **TreeSet[T]** | Sorted iteration, min/max, range queries | O(log n) | O(log n) | Sorted |
| **TreeMap[K,V]** | Sorted key-value pairs, range queries | O(log n) | O(log n) | Sorted by key |

O(eC) means "effectively constant" — O(log32 n), which is at most 7 operations even for a billion elements.

---

## Creating Collections

Every collection type has a corresponding factory function. Type parameters are inferred from the arguments:

```gala
import . "martianoff/gala/collection_immutable"
import . "martianoff/gala/go_interop"

// Sequences
val list = ListOf(1, 2, 3, 4, 5)
val arr  = ArrayOf("hello", "world")

// Sets
val hset = HashSetOf(1, 2, 3, 4, 5)
val tset = TreeSetOf(30, 10, 20)       // TreeSet(10, 20, 30) — sorted

// Maps
val hmap = HashMapOf(("a", 1), ("b", 2), ("c", 3))
val tmap = TreeMapOf(("cherry", 3), ("apple", 1), ("banana", 2))

// Empty collections with explicit type
val empty = EmptyList[int]()
val emptyArr = EmptyArray[string]()
val emptyMap = EmptyHashMap[string, int]()

// From slices
val fromSlice = ArrayFromSlice(SliceOf(10, 20, 30))

// Tabulate and Fill
val squares = ArrayTabulate(5, (i) => i * i)  // Array(0, 1, 4, 9, 16)
val zeros   = ArrayFill(3, 0)                  // Array(0, 0, 0)
```

---

## Core Operations: Map, Filter, FoldLeft, ForEach

These operations work identically across List, Array, and other sequence types. Lambda parameter types are inferred from the collection's element type — no annotations needed.

### Map — Transform Each Element

```gala
val names = ListOf("alice", "bob", "charlie")
val upper = names.Map((s) => strings.ToUpper(s))
// List("ALICE", "BOB", "CHARLIE")
```

### FlatMap — Transform and Flatten

```gala
val nums = ArrayOf(1, 2, 3)
val expanded = nums.FlatMap((x) => ArrayOf(x, x * 10))
// Array(1, 10, 2, 20, 3, 30)
```

### Filter — Keep Matching Elements

```gala
val nums = ListOf(1, 2, 3, 4, 5)
val evens = nums.Filter((x) => x % 2 == 0)      // List(2, 4)
val odds  = nums.FilterNot((x) => x % 2 == 0)   // List(1, 3, 5)
```

### FoldLeft — Reduce to a Single Value

The accumulator type is inferred from the zero value. No type annotation required:

```gala
val nums = ArrayOf(1, 2, 3, 4)
val sum = nums.FoldLeft(0, (acc, x) => acc + x)           // 10
val product = nums.FoldLeft(1, (acc, x) => acc * x)       // 24
val csv = nums.FoldLeft("", (acc, x) => acc + s"$x,")     // "1,2,3,4,"
```

### ForEach — Side Effects

```gala
val names = ListOf("Alice", "Bob")
names.ForEach((name) => {
    Println(s"Hello, $name!")
})
```

### Predicates: Exists, ForAll, Find, Count

```gala
val nums = ArrayOf(2, 4, 6, 8)

nums.Exists((x) => x > 5)          // true
nums.ForAll((x) => x % 2 == 0)     // true
nums.Find((x) => x > 5)            // Some(6)
nums.Count((x) => x > 4)           // 2
```

---

## Collect — Filter and Transform in One Pass

`Collect` applies a partial function: elements that do not match a case are skipped, matched elements are transformed. This combines `Filter` and `Map` into a single, efficient pass:

```gala
val nums = ArrayOf(1, 2, 3, 4, 5, 6)

// Keep even numbers, double them
val evenDoubled = nums.Collect({ case n if n % 2 == 0 => n * 2 })
Println(evenDoubled)  // Array(4, 8, 12)

// Extract values from Options
val options = ArrayOf(Some(1), None[int](), Some(2), None[int](), Some(3))
val values = options.Collect({ case Some(v) => v * 10 })
Println(values)  // Array(10, 20, 30)
```

`Collect` also works on HashMap entries:

```gala
val m = HashMapOf(("a", 1), ("b", 2), ("c", 3))
val highKeys = m.Collect((k, v) => if (v > 1) Some(k) else None[string]())
// Array("b", "c")
```

---

## Sorting: Sorted, SortWith, SortBy

All collections support three sorting operations. Sets and maps return `Array[T]` since they may not preserve insertion order:

```gala
// Natural ordering
val arr = ArrayOf(3, 1, 4, 1, 5, 9)
arr.Sorted()                              // Array(1, 1, 3, 4, 5, 9)

// Custom comparator (descending)
arr.SortWith((a, b) => a > b)             // Array(9, 5, 4, 3, 1, 1)

// Sort by key function
val words = ListOf("banana", "apple", "cherry")
words.SortBy((s) => len(s))               // List("apple", "banana", "cherry")

// Sets produce sorted Array
val set = HashSetOf(5, 3, 1)
set.Sorted()                               // Array(1, 3, 5)
```

---

## Conversion: ToSlice, MkString, ToList, ToArray

Collections can be converted to Go slices, other collection types, or formatted as strings:

```gala
val list = ListOf(1, 2, 3)

list.ToGoSlice()        // []int{1, 2, 3}
list.ToArray()          // Array(1, 2, 3)
list.MkString(", ")     // "1, 2, 3"
list.String()           // "List(1, 2, 3)"

val arr = ArrayOf(10, 20, 30)
arr.ToList()            // List(10, 20, 30)
```

---

## TreeMap: Sorted Maps with Range Queries

TreeMap maintains entries in sorted key order and supports efficient min/max and range operations:

```gala
val m = TreeMapOf(("a", 1), ("b", 2), ("c", 3), ("d", 4), ("e", 5))

m.MinKey()              // "a"
m.MaxKey()              // "e"
m.MinEntry()            // Tuple("a", 1)
m.MaxEntry()            // Tuple("e", 5)

// Range queries (inclusive)
m.Range("b", "d")       // TreeMap(b -> 2, c -> 3, d -> 4)
m.RangeFrom("c")        // TreeMap(c -> 3, d -> 4, e -> 5)
m.RangeTo("c")          // TreeMap(a -> 1, b -> 2, c -> 3)

// Functional operations
m.Filter((k, v) => v > 2)              // TreeMap(c -> 3, d -> 4, e -> 5)
m.MapValues((v) => v * 10)             // TreeMap(a -> 10, b -> 20, ...)
m.FoldLeftKV(0, (acc, k, v) => acc + v) // 15
```

---

## Lambda Type Inference in Collection Pipelines

GALA infers lambda parameter types from the collection's element type. This means you can write expressive pipelines without any type annotations:

```gala
val people = ArrayOf(
    Person("Alice", 30),
    Person("Bob", 25),
    Person("Charlie", 35),
)

// Every lambda parameter type is inferred
val result = people
    .Filter((p) => p.Age > 25)
    .Map((p) => p.Name)
    .SortBy((name) => name)
    .MkString(", ")

Println(result)  // "Alice, Charlie"
```

FoldLeft accumulator types are inferred from the zero value:

```gala
val nums = ArrayOf(1, 2, 3)
val sum = nums.FoldLeft(0, (acc, x) => acc + x)         // acc inferred as int
val csv = nums.FoldLeft("", (acc, x) => acc + s"$x ")   // acc inferred as string
```

---

## Sequence Pattern Matching on Collections

List and Array support destructuring in pattern matching:

```gala
val list = ListOf(1, 2, 3)

val result = list match {
    case Cons(head, _) => head     // Matches non-empty list, extracts head
    case Nil() => -1               // Matches empty list
    case _ => -2
}
```

Array supports sequence patterns with rest capture:

```gala
val arr = ArrayOf(1, 2, 3, 4, 5)

val res = arr match {
    case Array(first, second, _...) => s"First: $first, Second: $second"
    case _ => "Not enough elements"
}
```

---

## Mutable Collections

GALA also provides mutable collection variants in the `collection_mutable` package for performance-critical code that needs in-place mutation. Import them separately:

```gala
import . "martianoff/gala/collection_mutable"
```

The mutable collections share the same API surface (Map, Filter, ForEach, etc.) but modify the collection in place.

---

## Example: Data Processing Pipeline

```gala
package main

import . "martianoff/gala/collection_immutable"

struct Sale(Product string, Amount float64, Region string)

func main() {
    val sales = ArrayOf(
        Sale("Widget", 29.99, "North"),
        Sale("Gadget", 49.99, "South"),
        Sale("Widget", 19.99, "North"),
        Sale("Gadget", 39.99, "North"),
    )

    // Total revenue for North region widgets
    val northWidgetTotal = sales
        .Filter((s) => s.Region == "North")
        .Filter((s) => s.Product == "Widget")
        .FoldLeft(0.0, (acc, s) => acc + s.Amount)

    Println(f"North Widget Revenue: $$$northWidgetTotal%.2f")
    // North Widget Revenue: $49.98
}
```

---

## Further Reading

- [Immutability in GALA]({{ '/features/immutability/' | relative_url }}) — how `val` and immutable structs work
- [Type Inference]({{ '/features/type-inference/' | relative_url }}) — how lambda parameter and accumulator types are inferred
- [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) — destructuring collections and sealed types
