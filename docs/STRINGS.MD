# strings

Unified GALA string library. One package, two surfaces:

- **Free functions** over plain Go `string` — `TrimSpace`, `Split`, `Contains`, etc. — mirroring Go's `strings.X` names so call sites look like idiomatic Go.
- **`Str`** — an immutable wrapper that caches the Go string and lazily materialises an `Array[rune]` for functional methods. Chainable: every transform returns a new `Str`.
- **`StringBuilder`** — a mutable, O(n)-assembly builder for hot-path concatenation.

Pick the shape that fits the call site. Free functions are right for one-shots and `strings.X` migrations; `Str` is right for multi-step pipelines that benefit from caching the rune view.

## Import

```gala
import . "martianoff/gala/strings"
```

The GALA package shadows Go's stdlib `strings`. Inside this package the Go stdlib is aliased as `gostrings` — consumers never see that. From outside, just use the dot import.

## Quick start — free functions

```gala
val ok = HasPrefix("/api/v1/users", "/api/")        // true
val parts = Split("alpha,beta,gamma", ",")           // Array("alpha", "beta", "gamma")
val joined = Join(ArrayOf("a", "b", "c"), " - ")     // "a - b - c"
Println(s"trimmed: '${TrimSpace("   hello   ")}'")   // 'hello'
```

## Quick start — `Str` wrapper

```gala
val s = S("Hello, World!")
val cleaned = s.Trim().ToLower().ReplaceAll(",", "").ReplaceAll("!", "")
// cleaned.ToString() == "hello world"

val shifted = S("abc").Map((r rune) => r + 1)        // "bcd"
val vowels  = S("hello world").Filter((r rune) => r == 'a' || r == 'e' || r == 'i' || r == 'o' || r == 'u')

S("hello") match {
    case NonEmptyStr(head, tail) => Println(s"first=${string(head)} rest=${tail.ToString()}")
    case EmptyStr(_)             => Println("empty")
}
```

---

## Free function API

All functions live at package level. Names mirror Go's `strings.X` exactly so a `strings.X` → `X` substitution is the entire migration.

### Predicates

| Function | Signature | Notes |
|---|---|---|
| `Contains` | `(s, substr string) bool` | |
| `HasPrefix` | `(s, prefix string) bool` | |
| `HasSuffix` | `(s, suffix string) bool` | |
| `EqualFold` | `(a, b string) bool` | Unicode case-folding (rune-by-rune; ß≠"ss"). |
| `LastIndex` | `(s, substr string) int` | `-1` when absent. |

### Trim family

| Function | Signature |
|---|---|
| `TrimSpace` | `(s string) string` |
| `Trim` | `(s, cutset string) string` |
| `TrimLeft` | `(s, cutset string) string` |
| `TrimRight` | `(s, cutset string) string` |
| `TrimPrefix` | `(s, prefix string) string` |
| `TrimSuffix` | `(s, suffix string) string` |

### Case folding

| Function | Signature |
|---|---|
| `ToLower` | `(s string) string` |
| `ToUpper` | `(s string) string` |

### Build-up

| Function | Signature | Notes |
|---|---|---|
| `Repeat` | `(s string, count int) string` | Panics for negative `count` (Go's contract). |
| `Replace` | `(s, old, new string, n int) string` | `n < 0` ⇒ replace all. |
| `ReplaceAll` | `(s, old, new string) string` | |

### Split / Join

| Function | Signature | Notes |
|---|---|---|
| `Split` | `(s, sep string) Array[string]` | Empty `sep` ⇒ one entry per rune. |
| `SplitN` | `(s, sep string, n int) Array[string]` | `n > 0` caps; `n == 0` returns empty; `n < 0` returns all. |
| `Fields` | `(s string) Array[string]` | Splits on runs of Unicode whitespace. |
| `Join` | `(parts Array[string], sep string) string` | Joins an `Array[string]`. |
| `JoinStrs` | `(strs Array[Str], sep string) Str` | `Str`-shaped sibling of `Join`. |

`Split`, `SplitN`, and `Fields` return a GALA `Array[string]` (from `collection_immutable`), not a Go `[]string`. `Join` accepts an `Array[string]` for symmetry.

---

## `Str` — immutable wrapper

`Str` uses **lazy dual storage** — a `Lazy[Array[rune]]` and a cached Go `string` — providing:

- **O(1) creation** — rune array construction is deferred until first functional operation.
- **O(1) `ToString`, `Equals`, `Contains`** — direct access to the cached Go string.
- **Efficient chaining** — string-based operations (`ToUpper`, `Trim`, etc.) use the cached string directly without triggering rune conversion.
- **Functional methods** — `Map`, `Filter`, `Fold`, etc. evaluate the lazy rune array on first use and cache the result.
- **Pattern matching** — `NonEmptyStr` and `EmptyStr` extractors.

### Constructor

| Function | Description |
|---|---|
| `S(s string) Str` | Create `Str` from a Go string. |

### Basic operations

| Method | Description | Complexity |
|---|---|---|
| `Length() int` | Number of runes | O(1) after first rune access |
| `IsEmpty() bool` | True if zero length | O(1) |
| `NonEmpty() bool` | True if has characters | O(1) |
| `CharAt(index int) Option[rune]` | Rune at index | O(eC) |
| `ToString() string` | Convert to Go string | O(1) |
| `ToChars() Array[rune]` | Internal rune array (evaluates lazy) | O(n) once, then cached |

### Slicing

| Method | Description |
|---|---|
| `Substring(start, end int) Str` | Slice from start (inclusive) to end (exclusive) |
| `Take(n int) Str` | First n characters |
| `TakeRight(n int) Str` | Last n characters |
| `Drop(n int) Str` | Remove first n characters |
| `DropRight(n int) Str` | Remove last n characters |
| `SplitAt(index int) Tuple[Str, Str]` | Split at index |

### Case

| Method | Description |
|---|---|
| `ToUpper() Str` | Convert to uppercase |
| `ToLower() Str` | Convert to lowercase |
| `Capitalize() Str` | Uppercase first character |
| `Uncapitalize() Str` | Lowercase first character |
| `TitleCase() Str` | Capitalize each whitespace-separated word; whitespace runs collapse to a single space |

### Trimming

| Method | Description |
|---|---|
| `Trim() Str` | Remove leading/trailing whitespace |
| `TrimLeft() Str` | Remove leading whitespace |
| `TrimRight() Str` | Remove trailing whitespace |
| `TrimPrefix(prefix string) Str` | Remove prefix if present |
| `TrimSuffix(suffix string) Str` | Remove suffix if present |

### Replacement and shape

| Method | Description |
|---|---|
| `Replace(old, new string) Str` | Replace first occurrence |
| `ReplaceAll(old, new string) Str` | Replace all occurrences |
| `Reverse() Str` | Reverse the string |
| `Repeat(n int) Str` | Repeat n times |
| `PadLeft(length int, pad rune) Str` | Pad on left to reach length |
| `PadRight(length int, pad rune) Str` | Pad on right to reach length |
| `Center(length int, pad rune) Str` | Center with padding on both sides |

### Splitting

| Method | Description |
|---|---|
| `Split(sep string) Array[Str]` | Split by separator |
| `Lines() Array[Str]` | Split by newlines |
| `Words() Array[Str]` | Split by whitespace |

### Concatenation and comparison

| Method | Description |
|---|---|
| `Concat(other Str) Str` | Concatenate two strings |
| `Plus(other Str) Str` | Alias for `Concat` |
| `Equals(other Str) bool` | Exact equality |
| `EqualsIgnoreCase(other Str) bool` | Case-insensitive equality |
| `Compare(other Str) int` | Lexicographic comparison (-1, 0, 1) |

### Predicates

| Method | Description |
|---|---|
| `Contains(substr string) bool` | Contains substring |
| `ContainsAny(chars string) bool` | Contains any of the characters |
| `StartsWith(prefix string) bool` | Starts with prefix |
| `EndsWith(suffix string) bool` | Ends with suffix |
| `IsAlpha() bool` | All alphabetic |
| `IsNumeric() bool` | All digits |
| `IsAlphanumeric() bool` | All alphanumeric |
| `IsWhitespace() bool` | All whitespace |
| `IsUpper() bool` | All cased characters are uppercase |
| `IsLower() bool` | All cased characters are lowercase |

### Search

| Method | Description |
|---|---|
| `IndexOf(substr string) Option[int]` | Rune-index of first occurrence |
| `LastIndexOf(substr string) Option[int]` | Rune-index of last occurrence |
| `IndexOfChar(target rune) Option[int]` | Rune-index of rune |
| `Count(substr string) int` | Count occurrences |

### Functional operations

These methods delegate to `Array[rune]` for efficiency:

| Method | Description |
|---|---|
| `Map(f func(rune) rune) Str` | Transform each character |
| `Filter(p func(rune) bool) Str` | Keep matching characters |
| `FilterNot(p func(rune) bool) Str` | Remove matching characters |
| `ForEach(f func(rune))` | Apply function to each character |
| `Fold[U](zero U, f func(U, rune) U) U` | Reduce to single value |
| `Exists(p func(rune) bool) bool` | Any character matches |
| `ForAll(p func(rune) bool) bool` | All characters match |
| `Find(p func(rune) bool) Option[rune]` | First matching character |
| `ZipWithIndex() Array[Tuple[rune, int]]` | Pair with indices |

### Pattern matching extractors

```gala
// NonEmptyStr extracts (head, tail)
type NonEmptyStr struct {}
func (n NonEmptyStr) Unapply(s Str) Option[Tuple[rune, Str]]

// EmptyStr matches empty strings
type EmptyStr struct {}
func (e EmptyStr) Unapply(s Str) Option[bool]
```

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
```

---

## `StringBuilder` — mutable assembly

`StringBuilder` is a mutable string builder for efficient string concatenation, following the same mutable pattern as `collection_mutable`.

### Construction

```gala
val sb = NewStringBuilder()                    // empty
val sb2 = NewStringBuilderFrom(S("hello"))     // from Str
val sb3 = NewStringBuilderFromString("hello")  // from Go string
```

### Appending (chainable)

All append methods return `*StringBuilder` for chaining:

```gala
// Str-based
sb.Append(S("hello"))             // append Str
sb.AppendLine(S("hello"))         // append Str + newline
sb.AppendRune('*')                // append single rune

// Go string-based
sb.AppendString("hello")          // append Go string
sb.AppendStringLine("hello")      // append Go string + newline

// Chaining
val result = NewStringBuilder()
    .AppendString("hello")
    .AppendString(" ")
    .AppendString("world")
    .ToString()  // "hello world"
```

### Output

```gala
sb.ToStr()     // returns Str
sb.ToString()  // returns Go string
sb.String()    // Go Stringer interface
```

### State

```gala
sb.Length()    // byte length
sb.RuneCount() // character count
sb.IsEmpty()   // true if no content
sb.NonEmpty()  // true if has content
sb.Reset()     // clears content
```

---

## Examples

### Splitting and joining

```gala
val parts = S("a,b,c").Split(",")
// parts == Array[Str]{S("a"), S("b"), S("c")}

val (left, right) = S("hello").SplitAt(2)
// left.ToString() == "he", right.ToString() == "llo"

val joined = JoinStrs(ArrayOf(S("a"), S("b"), S("c")), "-")
// joined.ToString() == "a-b-c"

val lines = S("line1\nline2\nline3").Lines()
// lines.Length() == 3

val words = S("hello  world\tfoo").Words()
// words.Length() == 3
```

### Padding

```gala
S("42").PadLeft(5, rune(48))   // "00042" (pad with '0')
S("hi").PadRight(5, rune(45))  // "hi---" (pad with '-')
S("a").Center(5, rune(45))     // "--a--"
```

### Predicate methods

```gala
S("hello").IsAlpha()           // true
S("12345").IsNumeric()         // true
S("hello123").IsAlphanumeric() // true
S("   \t").IsWhitespace()      // true
S("HELLO").IsUpper()           // true
S("hello").IsLower()           // true
```

---

## Performance benchmarks

Benchmark results comparing GALA `Str` to Go native string operations on a 1,000-character ASCII string.

### Running the benchmarks

```shell
# GALA Str benchmark
bazel run //strings:perf_gala

# Go native benchmark
bazel run //strings:perf_go
```

### `Str` vs Go string (ns/op) — 1,000 characters

| Operation | GALA Str | Go Native | Ratio | Notes |
|-----------|----------:|----------:|------:|-------|
| Creation (1K) | 220 | 807 | **3.7x faster** | Lazy: defers rune conversion |
| Creation (100K) | 8,291 | 63,458 | **7.7x faster** | Lazy: no upfront allocation |
| Length | 2 | 0 | ~same | O(1) after first rune access |
| ToUpper | 1,323 | 1,205 | 1.1x | Direct `strings.ToUpper` on cached `str` |
| ToLower | 599 | 551 | 1.1x | Direct `strings.ToLower` on cached `str` |
| Trim | 57 | 10 | 5.7x | Direct `strings.TrimSpace` on cached `str` |
| ReplaceAll | 1,615 | 1,455 | 1.1x | Direct `strings.ReplaceAll` on cached `str` |
| Contains | 6 | 6 | **1.0x** | Direct `strings.Contains` on cached `str` |
| IndexOf | 19 | 7 | 2.7x | Direct `strings.Index` on cached `str` |
| Split (10 words) | 771 | 60 | 13x | `ArrayTabulate` + `Str` wrapping |
| Split (1K words) | 64,801 | 6,544 | 10x | `ArrayTabulate` + `Str` wrapping |
| Reverse | 13,365 | 2,912 | 4.6x | Evaluates lazy runes, delegates to `Array.Reverse` |
| Equals | 11 | 10 | **1.1x** | Direct `==` on cached `str` |
| Concat | 171 | 140 | **1.2x** | Lazy rune concat + string concat |

### Functional operations (ns/op) — 1,000 characters

| Operation | GALA Str | Go Native | Ratio | Notes |
|-----------|----------:|----------:|------:|-------|
| Map (ToUpper) | 14,388 | 3,576 | 4.0x | Evaluates lazy runes, delegates to `Array.Map` |
| Filter (IsLetter) | 15,019 | 3,307 | 4.5x | Evaluates lazy runes, delegates to `Array.Filter` |
| ForEach+Count | 3,589 | 180 | 20x | `Array[rune]` iteration with function call overhead |
| IsAlpha (ForAll) | 3,474 | 401 | 8.7x | Delegates to `Array[rune].ForAll` |
| Substring | 12,598 | 876 | 14x | Evaluates lazy runes, slices array + rebuilds string |

### `StringBuilder` vs `strings.Builder` (ns/op)

| Operation | GALA StringBuilder | Go Builder | Ratio | Notes |
|-----------|-------------------:|-----------:|------:|-------|
| Append 100x"hello" | 1,671 | 256 | 6.5x | Stores raw strings, `strings.Join` on `ToString` |
| Append 10000x"hello" | 212,014 | 29,671 | 7.1x | O(n) assembly via `strings.Join` |
| AppendRune 10000x'x' | 294,326 | 18,009 | 16x | `string(r)` conversion + mutable array append |

### Key performance insights

**Near Go-native (1.0–1.2x):**
- `Contains()`, `Equals()`, `Concat()` — direct operations on cached Go string.
- `ToUpper()`, `ToLower()`, `ReplaceAll()` — delegate to Go's `strings`, lazy runes not triggered.
- `Creation` — **faster than Go native** thanks to lazy rune conversion.

**Moderate overhead (4–14x):**
- `Map`, `Filter`, `Reverse` — immutable `Array[rune]` trie overhead (first call evaluates lazy).
- `Split` — `ArrayTabulate` builds `Array[Str]` efficiently via `arrayBuilder`.
- `StringBuilder` — raw string storage with `strings.Join` assembly.

**Higher overhead (16–20x):**
- `ForEach+Count` — per-element function call overhead on `Array[rune]`.
- `AppendRune` — `string(r)` conversion per rune + mutable array growth.

---

## Design notes

### Lazy dual storage architecture

`Str` stores a `Lazy[Array[rune]]` and a cached Go `string`:

```gala
type Str struct {
    runes *lazy.Lazy[Array[rune]]   // deferred until first functional op
    str string                       // always available
}
```

- **Cached string** enables O(1) `ToString()`, `Equals()`, `Contains()`, `IndexOf()`, and near-native `ToUpper()`, `ToLower()`, `ReplaceAll()`.
- **Lazy rune array** is only evaluated when functional operations (`Map`, `Filter`, `CharAt`, etc.) are called. Once evaluated, the result is cached for subsequent calls.

Construction strategies:
- `strFromString(s)` — defers rune conversion via `lazy.New`.
- `strFromRunes(runes)` — already-evaluated via `lazy.Of`.
- String-based ops (`ToUpper`, `Trim`, etc.) — return new `Str` with deferred runes.
- `Concat` — defers rune concatenation, immediately concatenates strings.

### `StringBuilder` optimisation

`StringBuilder` stores raw `string` parts instead of `Str` values:
- `Append` / `AppendString` store Go strings directly (no rune conversion).
- `ToString()` assembles via `strings.Join` — O(n) instead of O(n^2).
- `ToStr()` converts the final string to `Str` once.
