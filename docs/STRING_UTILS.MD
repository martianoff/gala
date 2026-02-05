# string_utils

Rich, immutable string operations for GALA with full functional programming support.

## Import

```gala
import (
    . "martianoff/gala/string_utils"
)
```

## Overview

The `Str` type wraps strings as `Array[rune]` internally, providing:

- **O(1) length** - No UTF-8 counting on each call
- **Efficient chaining** - Operations work directly on runes without repeated conversions
- **Functional methods** - Map, Filter, Fold, etc. delegate to `Array[T]` methods
- **Pattern matching** - `NonEmptyStr` and `EmptyStr` extractors

## Quick Start

```gala
// Create a Str
val s = S("Hello, World!")

// Chain operations
val result = s.Trim().ToLower().ReplaceAll(",", "").ReplaceAll("!", "")
// result.ToString() == "hello world"

// Functional operations
val upper = S("hello").Map((r rune) => unicode.ToUpper(r))
val noSpaces = S("hello world").Filter((r rune) => r != rune(32))

// Pattern matching
S("hello") match {
    case NonEmptyStr(head, tail) => fmt.Printf("First: %c\n", head)
    case EmptyStr(_) => fmt.Println("Empty")
}
```

## API Reference

### Constructor

| Function | Description |
|----------|-------------|
| `S(s string) Str` | Create Str from Go string |

### Basic Operations

| Method | Description | Complexity |
|--------|-------------|------------|
| `Length() int` | Number of runes | O(1) |
| `IsEmpty() bool` | True if zero length | O(1) |
| `NonEmpty() bool` | True if has characters | O(1) |
| `CharAt(index int) Option[rune]` | Rune at index | O(1) |
| `ToString() string` | Convert to Go string | O(n) |

### Slicing

| Method | Description |
|--------|-------------|
| `Substring(start, end int) Str` | Slice from start (inclusive) to end (exclusive) |
| `Take(n int) Str` | First n characters |
| `TakeRight(n int) Str` | Last n characters |
| `Drop(n int) Str` | Remove first n characters |
| `DropRight(n int) Str` | Remove last n characters |

### Case Transformations

| Method | Description |
|--------|-------------|
| `ToUpper() Str` | Convert to uppercase |
| `ToLower() Str` | Convert to lowercase |
| `Capitalize() Str` | Uppercase first character |
| `Uncapitalize() Str` | Lowercase first character |

### Trimming

| Method | Description |
|--------|-------------|
| `Trim() Str` | Remove leading/trailing whitespace |
| `TrimLeft() Str` | Remove leading whitespace |
| `TrimRight() Str` | Remove trailing whitespace |
| `TrimPrefix(prefix string) Str` | Remove prefix if present |
| `TrimSuffix(suffix string) Str` | Remove suffix if present |

### Replacement

| Method | Description |
|--------|-------------|
| `Replace(old, new string) Str` | Replace first occurrence |
| `ReplaceAll(old, new string) Str` | Replace all occurrences |

### Other Transformations

| Method | Description |
|--------|-------------|
| `Reverse() Str` | Reverse the string |
| `Repeat(n int) Str` | Repeat n times |
| `PadLeft(length int, pad rune) Str` | Pad on left to reach length |
| `PadRight(length int, pad rune) Str` | Pad on right to reach length |
| `Center(length int, pad rune) Str` | Center with padding on both sides |

### Splitting & Joining

| Method | Description |
|--------|-------------|
| `Split(sep string) Array[Str]` | Split by separator |
| `SplitAt(index int) Tuple[Str, Str]` | Split at index |
| `Lines() Array[Str]` | Split by newlines |
| `Words() Array[Str]` | Split by whitespace |
| `Join(strs Array[Str], sep string) Str` | Join with separator |

### Concatenation

| Method | Description |
|--------|-------------|
| `Concat(other Str) Str` | Concatenate two strings |
| `Plus(other Str) Str` | Alias for Concat |

### Predicates

| Method | Description |
|--------|-------------|
| `Contains(substr string) bool` | Contains substring |
| `ContainsAny(chars string) bool` | Contains any of the characters |
| `StartsWith(prefix string) bool` | Starts with prefix |
| `EndsWith(suffix string) bool` | Ends with suffix |
| `IsAlpha() bool` | All alphabetic (uses `ForAll`) |
| `IsNumeric() bool` | All digits |
| `IsAlphanumeric() bool` | All alphanumeric |
| `IsWhitespace() bool` | All whitespace |
| `IsUpper() bool` | All cased characters are uppercase |
| `IsLower() bool` | All cased characters are lowercase |

### Search

| Method | Description |
|--------|-------------|
| `IndexOf(substr string) Option[int]` | Index of first occurrence |
| `LastIndexOf(substr string) Option[int]` | Index of last occurrence |
| `IndexOfChar(target rune) Option[int]` | Index of rune |
| `Count(substr string) int` | Count occurrences |

### Comparison

| Method | Description |
|--------|-------------|
| `Equals(other Str) bool` | Exact equality |
| `EqualsIgnoreCase(other Str) bool` | Case-insensitive equality |
| `Compare(other Str) int` | Lexicographic comparison (-1, 0, 1) |

### Functional Operations

These methods delegate to `Array[rune]` for efficiency:

| Method | Description |
|--------|-------------|
| `Map(f func(rune) rune) Str` | Transform each character |
| `Filter(p func(rune) bool) Str` | Keep matching characters |
| `FilterNot(p func(rune) bool) Str` | Remove matching characters |
| `ForEach(f func(rune))` | Apply function to each character |
| `Fold[U](zero U, f func(U, rune) U) U` | Reduce to single value |
| `Exists(p func(rune) bool) bool` | Any character matches |
| `ForAll(p func(rune) bool) bool` | All characters match |
| `Find(p func(rune) bool) Option[rune]` | First matching character |
| `ZipWithIndex() Array[Tuple[rune, int]]` | Pair with indices |

### Conversion

| Method | Description |
|--------|-------------|
| `ToChars() Array[rune]` | Get internal rune array |
| `ToString() string` | Convert to Go string |

### Pattern Matching Extractors

```gala
// NonEmptyStr extracts (head, tail)
type NonEmptyStr struct {}
func (n NonEmptyStr) Unapply(s Str) Option[Tuple[rune, Str]]

// EmptyStr matches empty strings
type EmptyStr struct {}
func (e EmptyStr) Unapply(s Str) Option[bool]
```

## Examples

### Chained Transformations

```gala
val cleaned = S("  Hello, World!  ")
    .Trim()
    .ToLower()
    .ReplaceAll(",", "")
    .ReplaceAll("!", "")
// cleaned.ToString() == "hello world"
```

### Functional Operations

```gala
// Map: shift each character
val shifted = S("abc").Map((r rune) => r + 1)
// shifted.ToString() == "bcd"

// Filter: keep only vowels
val vowels = S("hello world").Filter((r rune) => {
    return r == rune(97) || r == rune(101) || r == rune(105) || r == rune(111) || r == rune(117)
})
// vowels.ToString() == "eoo"

// Fold: count characters
val count = S("hello").Fold(0, (acc int, r rune) => acc + 1)
// count == 5
```

### Predicate Methods

```gala
// These delegate to ForAll for efficiency
S("hello").IsAlpha()        // true
S("12345").IsNumeric()      // true
S("hello123").IsAlphanumeric() // true
S("   \t").IsWhitespace()   // true
S("HELLO").IsUpper()        // true
S("hello").IsLower()        // true
```

### Pattern Matching

```gala
func describe(s Str) string = s match {
    case EmptyStr(_) => "empty"
    case NonEmptyStr(head, tail) => {
        if tail.IsEmpty() {
            return "single character: " + string(head)
        }
        return "starts with " + string(head) + ", rest: " + tail.ToString()
    }
}

describe(S(""))       // "empty"
describe(S("a"))      // "single character: a"
describe(S("hello"))  // "starts with h, rest: ello"
```

### Splitting and Joining

```gala
// Split
val parts = S("a,b,c").Split(",")
// parts == Array[Str]{S("a"), S("b"), S("c")}

// SplitAt
val (left, right) = S("hello").SplitAt(2)
// left.ToString() == "he", right.ToString() == "llo"

// Join
val joined = Join(ArrayOf(S("a"), S("b"), S("c")), "-")
// joined.ToString() == "a-b-c"

// Lines
val lines = S("line1\nline2\nline3").Lines()
// lines.Length() == 3

// Words
val words = S("hello  world\tfoo").Words()
// words.Length() == 3
```

### Padding

```gala
S("42").PadLeft(5, rune(48))   // "00042" (pad with '0')
S("hi").PadRight(5, rune(45))  // "hi---" (pad with '-')
S("a").Center(5, rune(45))     // "--a--"
```

## Design Notes

### Why Store `Array[rune]`?

Most string operations in GALA are character-based (Map, Filter, Reverse, etc.). Storing `Array[rune]` enables:

1. **O(1) Length** - `s.runes.Length()` vs O(n) UTF-8 counting
2. **Direct delegation** - `Map`, `Filter`, `Fold` call array methods directly
3. **No conversion overhead** - Chained operations stay in rune representation
4. **Simpler implementation** - Predicate methods become one-liners

### Trade-off

`ToString()` is O(n) instead of O(1), but this is called infrequently (usually at output boundaries), while character operations are common throughout string processing pipelines.
