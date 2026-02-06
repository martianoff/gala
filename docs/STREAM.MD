# Stream Package

The `stream` package provides lazy, potentially infinite sequences for GALA. Elements are computed on demand using thunks (deferred functions), enabling efficient processing of large or infinite data sets.

## Import

```gala
import . "martianoff/gala/stream"
```

## Key Concepts

### Laziness
All transformation operations (`Map`, `Filter`, `Take`, etc.) are **lazy** - they don't compute elements until forced by a terminal operation.

### Terminal vs Transformation Operations
- **Transformation operations** return new streams without forcing evaluation
- **Terminal operations** (`ToArray`, `ForEach`, `Fold`, etc.) force evaluation

### Infinite Streams
Streams can represent infinite sequences. Use `Take`, `TakeWhile`, or other limiting operations before terminal operations to avoid infinite loops.

---

## Creating Streams

### From Values

```gala
// From variadic arguments
val s1 = Of(1, 2, 3, 4, 5)

// Empty stream
val s2 = Empty[int]()

// Single element with lazy tail
val s3 = NewCons[int](1, () => NewCons[int](2, () => Empty[int]()))
```

### From Collections

```gala
// From Array
val arr = ArrayOf(1, 2, 3)
val s1 = FromArray(arr)

// From List
val list = ListOf(1, 2, 3)
val s2 = FromList(list)
```

### Numeric Ranges

```gala
// Range from start (inclusive) to end (exclusive)
val s1 = Range(1, 10)          // 1, 2, 3, ..., 9

// Range with step
val s2 = RangeStep(0, 10, 2)   // 0, 2, 4, 6, 8
val s3 = RangeStep(10, 0, -2)  // 10, 8, 6, 4, 2
```

### Infinite Streams

```gala
// Infinite sequence starting from n
val naturals = From(1)              // 1, 2, 3, 4, ...

// Infinite repetition
val ones = Repeat(1)                // 1, 1, 1, 1, ...

// Infinite iteration
val powers = Iterate[int](1, (x int) => x * 2)  // 1, 2, 4, 8, 16, ...

// Infinite evaluation
val randoms = Continually(() => rand.Int())     // random, random, ...
```

### Unfold (Generate from State)

`Unfold` creates a stream by repeatedly applying a function that returns `Option[(value, nextState)]`:

```gala
// Fibonacci sequence
val fibs = Unfold[int, Tuple[int, int]](
    Tuple[int, int](V1 = 0, V2 = 1),
    (state Tuple[int, int]) => Some(Tuple[int, Tuple[int, int]](
        V1 = state.V1,                                                     // emit current
        V2 = Tuple[int, int](V1 = state.V2, V2 = state.V1 + state.V2),     // next state
    )),
)
// Result: 0, 1, 1, 2, 3, 5, 8, 13, ...

// Countdown (finite)
val countdown = Unfold[int, int](
    10,
    (n int) => {
        if (n >= 0) {
            return Some(Tuple[int, int](V1 = n, V2 = n - 1))
        }
        return None[Tuple[int, int]]()
    },
)
// Result: 10, 9, 8, ..., 0
```

### Suspend (Fully Lazy)

`Suspend` creates a stream where even the existence of elements is deferred:

```gala
val s = Suspend[int](() => expensiveComputation())
```

---

## Transformation Operations (Lazy)

All transformation operations return new streams without forcing evaluation.

### Map

Transform each element:

```gala
val doubled = Of(1, 2, 3).Map[int]((x int) => x * 2)
// Result: 2, 4, 6
```

### FlatMap

Apply function returning stream, then flatten:

```gala
val s = Of(1, 2, 3).FlatMap[int]((x int) => Of(x, x * 10))
// Result: 1, 10, 2, 20, 3, 30
```

### Filter / FilterNot

Keep elements matching (or not matching) predicate:

```gala
val evens = Of(1, 2, 3, 4, 5).Filter((x int) => x % 2 == 0)
// Result: 2, 4

val odds = Of(1, 2, 3, 4, 5).FilterNot((x int) => x % 2 == 0)
// Result: 1, 3, 5
```

### Take / TakeWhile

Limit stream length:

```gala
val first3 = Of(1, 2, 3, 4, 5).Take(3)
// Result: 1, 2, 3

val lessThan4 = Of(1, 2, 3, 4, 5).TakeWhile((x int) => x < 4)
// Result: 1, 2, 3
```

### Drop / DropWhile

Skip elements:

```gala
val after2 = Of(1, 2, 3, 4, 5).Drop(2)
// Result: 3, 4, 5

val from3 = Of(1, 2, 3, 4, 5).DropWhile((x int) => x < 3)
// Result: 3, 4, 5
```

### Zip / ZipWithIndex

Combine streams:

```gala
val s1 = Of(1, 2, 3)
val s2 = Of("a", "b", "c")
val zipped = s1.Zip[string](s2)
// Result: (1, "a"), (2, "b"), (3, "c")

val indexed = Of("a", "b", "c").ZipWithIndex()
// Result: ("a", 0), ("b", 1), ("c", 2)
```

### Concat / Prepend / Append

Combine streams:

```gala
val s = Of(1, 2).Concat(Of(3, 4))     // 1, 2, 3, 4
val s2 = Of(2, 3).Prepend(1)          // 1, 2, 3 (O(1))
val s3 = Of(1, 2).Append(3)           // 1, 2, 3 (must traverse)
```

### Intersperse

Insert separator between elements:

```gala
val s = Of(1, 2, 3).Intersperse(0)
// Result: 1, 0, 2, 0, 3
```

### Distinct

Remove duplicates (uses O(n) memory):

```gala
val s = Of(1, 2, 1, 3, 2).Distinct()
// Result: 1, 2, 3
```

### Slice

Get a range of elements:

```gala
val s = Of(0, 1, 2, 3, 4, 5).Slice(1, 4)
// Result: 1, 2, 3
```

### Scan

Cumulative results:

```gala
val s = Of(1, 2, 3).Scan[int](0, (acc int, x int) => acc + x)
// Result: 0, 1, 3, 6
```

### Collect

Apply partial function, keeping defined results:

```gala
val s = Of(1, 2, 3, 4).Collect[int]((x int) => {
    if (x % 2 == 0) Some(x * 10) else None[int]()
})
// Result: 20, 40
```

### Split Operations

```gala
val s = Of(1, 2, 3, 4, 5)

// SplitAt: split at position
val (first, rest) = s.SplitAt(2)  // (1, 2), (3, 4, 5)

// Span: split at first non-matching
val (matching, rest) = s.Span((x int) => x < 3)  // (1, 2), (3, 4, 5)

// Partition: split by predicate
val (evens, odds) = s.Partition((x int) => x % 2 == 0)  // (2, 4), (1, 3, 5)
```

---

## Terminal Operations (Force Evaluation)

**WARNING:** Terminal operations on infinite streams will not terminate!

### ToArray / ToList

Convert to collection:

```gala
val arr = Of(1, 2, 3).ToArray()   // Array[int]
val list = Of(1, 2, 3).ToList()   // List[int]
```

### Fold / Reduce

Aggregate values:

```gala
val sum = Of(1, 2, 3, 4, 5).Fold[int](0, (acc int, x int) => acc + x)
// Result: 15

val product = Of(1, 2, 3, 4).Reduce((a int, b int) => a * b)
// Result: Some(24)
```

### ForEach

Apply side-effecting function:

```gala
Of(1, 2, 3).ForEach((x int) => fmt.Println(x))
```

### Count / Length / Size

Get number of elements (all equivalent):

```gala
val n = Of(1, 2, 3).Count()  // 3
```

### Find / Exists / ForAll

Search and test:

```gala
val found = Of(1, 2, 3, 4).Find((x int) => x > 2)  // Some(3)

val hasEven = Of(1, 2, 3).Exists((x int) => x % 2 == 0)  // true

val allPositive = Of(1, 2, 3).ForAll((x int) => x > 0)   // true
```

### Contains / IndexOf / IndexWhere

Search for elements:

```gala
val hasTwo = Of(1, 2, 3).Contains(2)          // true
val idx = Of(10, 20, 30).IndexOf(20)          // 1
val idx2 = Of(1, 2, 3).IndexWhere((x int) => x > 1)  // 1
```

### MkString

Join elements into string:

```gala
val s = Of(1, 2, 3).MkString(", ")  // "1, 2, 3"
```

---

## Accessing Elements

```gala
val s = Of(10, 20, 30)

// Head (first element)
val first = s.Head()              // Option[int] = Some(10)
val firstOrDefault = s.HeadOrElse(0)  // int = 10

// Tail (rest of stream)
val rest = s.Tail()               // Stream[int] = 20, 30

// By index
val second = s.Get(1)             // Option[int] = Some(20)

// Check emptiness
val isEmpty = s.IsEmpty()         // false
val hasElements = s.NonEmpty()    // true
```

---

## Pattern Matching

The package provides two styles of pattern matching for streams.

### Type-Based Extractors

Use `StreamCons` and `StreamNil` extractors for type-based matching:

```gala
val result = myStream match {
    case StreamCons(head, tail) => {
        // head is T, tail is Stream[T]
        fmt.Printf("Head: %v, Tail length: %d\n", head, tail.Count())
        return head
    }
    case StreamNil() => {
        fmt.Println("Empty stream")
        return defaultValue
    }
    case _ => defaultValue
}
```

### Sequence Pattern Matching

Stream implements the `Seq[T]` interface, enabling sequence pattern matching syntax:

```gala
val s = Of(1, 2, 3, 4, 5)

val result = s match {
    // Match first two elements, rest goes into 'tail'
    case Stream(first, second, tail...) => {
        fmt.Printf("First: %d, Second: %d, Rest size: %d\n", first, second, tail.Size())
        return first + second
    }
    // Match single element
    case Stream(only) => only
    // Match empty stream
    case Stream() => 0
    case _ => -1
}
// Result: first=1, second=2, tail.Size()=3, returns 3
```

**Seq Interface Methods:**

| Method | Description |
|--------|-------------|
| `Size()` | Number of elements (terminal operation) |
| `Get(n)` | Element at index n, panics if out of bounds |
| `GetOption(n)` | Element at index n as Option |
| `SeqDrop(n)` | Drop first n elements (for rest pattern) |

**WARNING:** Sequence pattern matching on infinite streams will not terminate! The `Size()` method is called to determine the number of elements, which requires traversing the entire stream. Always use `Take` or similar limiting operations before sequence pattern matching:

```gala
// WRONG: Will hang on infinite stream
val naturals = From(1)
val result = naturals match {
    case Stream(a, b, rest...) => a + b  // Never terminates!
}

// CORRECT: Limit first
val result = From(1).Take(10) match {
    case Stream(a, b, rest...) => a + b  // Returns 3
}
```

---

## Complex Examples

### Sum of Squares of Even Numbers

```gala
val result = From(1)
    .Take(10)
    .Filter((x int) => x % 2 == 0)
    .Map[int]((x int) => x * x)
    .Fold[int](0, (acc int, x int) => acc + x)
// 2² + 4² + 6² + 8² + 10² = 220
```

### Prime Number Sieve

```gala
func sieve(s Stream[int]) Stream[int] {
    val head = s.Head()
    if head.IsDefined() {
        val p = head.Get()
        return NewCons[int](p, () => sieve(s.Tail().Filter((x int) => x % p != 0)))
    }
    return Empty[int]()
}

val primes = sieve(From(2))
val first10Primes = primes.Take(10).ToArray()
// 2, 3, 5, 7, 11, 13, 17, 19, 23, 29
```

### Fibonacci Sequence

```gala
val fibs = Unfold[int, Tuple[int, int]](
    Tuple[int, int](V1 = 0, V2 = 1),
    (state Tuple[int, int]) => Some(Tuple[int, Tuple[int, int]](
        V1 = state.V1,
        V2 = Tuple[int, int](V1 = state.V2, V2 = state.V1 + state.V2),
    )),
)
val first10 = fibs.Take(10).ToArray()
// 0, 1, 1, 2, 3, 5, 8, 13, 21, 34
```

---

## Method Reference

### Constructors

| Function | Description |
|----------|-------------|
| `Empty[T]()` | Empty stream |
| `Of[T](elements...)` | Stream from values |
| `NewCons[T](head, tailThunk)` | Cons cell with lazy tail |
| `FromArray[T](arr)` | Stream from Array |
| `FromList[T](list)` | Stream from List |
| `Range(start, end)` | Integer range [start, end) |
| `RangeStep(start, end, step)` | Range with step |
| `From(n)` | Infinite integers from n |
| `Repeat[T](elem)` | Infinite repetition |
| `Iterate[T](seed, f)` | Infinite iteration |
| `Continually[T](f)` | Infinite evaluation |
| `Unfold[T, S](seed, f)` | Generate from state |
| `Suspend[T](thunk)` | Fully lazy stream |

### Transformation (Lazy)

| Method | Description |
|--------|-------------|
| `Map[U](f)` | Transform elements |
| `FlatMap[U](f)` | Transform and flatten |
| `Filter(p)` | Keep matching elements |
| `FilterNot(p)` | Keep non-matching elements |
| `Take(n)` | First n elements |
| `TakeWhile(p)` | Elements while predicate holds |
| `Drop(n)` | Skip first n elements |
| `DropWhile(p)` | Skip while predicate holds |
| `Zip[U](other)` | Pair with another stream |
| `ZipWithIndex()` | Pair with indices |
| `Concat(other)` | Append another stream |
| `Prepend(elem)` | Add element at front |
| `Append(elem)` | Add element at end |
| `Intersperse(sep)` | Insert separator |
| `Distinct()` | Remove duplicates |
| `Slice(start, end)` | Substream |
| `Scan[U](zero, f)` | Cumulative results |
| `Collect[U](pf)` | Partial function application |
| `SplitAt(n)` | Split at position |
| `Span(p)` | Split at first non-match |
| `Partition(p)` | Split by predicate |

### Terminal (Force Evaluation)

| Method | Description |
|--------|-------------|
| `ToArray()` | Convert to Array |
| `ToList()` | Convert to List |
| `Fold[U](zero, f)` | Left fold |
| `Reduce(f)` | Reduce without initial value |
| `ForEach(f)` | Side-effecting iteration |
| `Count()` / `Length()` / `Size()` | Element count |
| `Find(p)` | First matching element |
| `Exists(p)` | Any element matches |
| `ForAll(p)` | All elements match |
| `Contains(elem)` | Element exists |
| `IndexOf(elem)` | Index of element |
| `IndexWhere(p)` | Index of first match |
| `MkString(sep)` | Join to string |

### Access

| Method | Description |
|--------|-------------|
| `Head()` | First element as Option |
| `HeadOrElse(default)` | First element or default |
| `Tail()` | Rest of stream |
| `Get(n)` | Element at index (panics if out of bounds) |
| `GetOption(n)` | Element at index as Option |
| `IsEmpty()` | Check if empty |
| `NonEmpty()` | Check if non-empty |

### Seq Interface (Sequence Pattern Matching)

| Method | Description |
|--------|-------------|
| `Size()` | Number of elements (same as Count) |
| `Get(n)` | Element at index (panics if out of bounds) |
| `SeqDrop(n)` | Drop first n elements (returns Stream) |

### Pattern Matching

| Extractor | Description |
|-----------|-------------|
| `StreamCons[T]` | Matches non-empty stream, extracts (head, tail) |
| `StreamNil[T]` | Matches empty stream |
