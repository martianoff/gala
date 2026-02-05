# Standard Library Additions - Implementation Plan

This document outlines the implementation plan for six new standard library packages in GALA.

## Overview

| Package | Purpose | Priority | Complexity |
|---------|---------|----------|------------|
| `stream` | Lazy infinite sequences | High | Medium |
| `string_utils` | Rich string operations | High | Low |
| `time_utils` | Immutable date/time utilities | High | Medium |
| `json` | Type-safe JSON serialization | Medium | High |
| `parser` | Parser combinators | Medium | High |
| `io` | IO monad for referential transparency | Low | High |

## Directory Structure

Each package follows the established pattern:

```
gala_simple/
├── stream/
│   ├── BUILD.bazel
│   ├── stream.gala
│   └── stream_test.gala
├── string_utils/
│   ├── BUILD.bazel
│   ├── strings.gala
│   └── strings_test.gala
├── time_utils/
│   ├── BUILD.bazel
│   ├── instant.gala
│   ├── duration.gala
│   └── time_test.gala
├── json/
│   ├── BUILD.bazel
│   ├── json.gala
│   ├── encoder.gala
│   ├── decoder.gala
│   └── json_test.gala
├── parser/
│   ├── BUILD.bazel
│   ├── parser.gala
│   ├── combinators.gala
│   └── parser_test.gala
└── io/
    ├── BUILD.bazel
    ├── io.gala
    └── io_test.gala
```

---

## 1. Package: `stream`

**Purpose**: Lazy, potentially infinite sequences with functional operations.

### API Design

```gala
package stream

import (
    . "martianoff/gala/std"
)

// Core type - lazy sequence
type Stream[T any] struct {
    head func() Option[T]
    tail func() Stream[T]
}

// Constructors
func Empty[T any]() Stream[T]
func Of[T any](values ...T) Stream[T]
func Cons[T any](head T, tail func() Stream[T]) Stream[T]
func Continually[T any](elem func() T) Stream[T]
func Iterate[T any](seed T, f func(T) T) Stream[T]
func Unfold[T any, S any](seed S, f func(S) Option[Tuple2[T, S]]) Stream[T]
func Range(start int, end int) Stream[int]
func RangeStep(start int, end int, step int) Stream[int]
func From(start int) Stream[int]  // Infinite sequence
func Repeat[T any](elem T) Stream[T]  // Infinite repetition

// Operations (all lazy except terminal operations)
func (s Stream[T]) Map[U any](f func(T) U) Stream[U]
func (s Stream[T]) FlatMap[U any](f func(T) Stream[U]) Stream[U]
func (s Stream[T]) Filter(p func(T) bool) Stream[T]
func (s Stream[T]) Take(n int) Stream[T]
func (s Stream[T]) TakeWhile(p func(T) bool) Stream[T]
func (s Stream[T]) Drop(n int) Stream[T]
func (s Stream[T]) DropWhile(p func(T) bool) Stream[T]
func (s Stream[T]) Zip[U any](other Stream[U]) Stream[Tuple2[T, U]]
func (s Stream[T]) ZipWithIndex() Stream[Tuple2[T, int]]
func (s Stream[T]) Concat(other Stream[T]) Stream[T]
func (s Stream[T]) Distinct() Stream[T]
func (s Stream[T]) Intersperse(sep T) Stream[T]

// Terminal operations (force evaluation)
func (s Stream[T]) Head() Option[T]
func (s Stream[T]) HeadOrElse(default T) T
func (s Stream[T]) Tail() Stream[T]
func (s Stream[T]) IsEmpty() bool
func (s Stream[T]) ToArray() Array[T]
func (s Stream[T]) ToList() List[T]
func (s Stream[T]) ForEach(f func(T))
func (s Stream[T]) Fold[U any](zero U, f func(U, T) U) U
func (s Stream[T]) Reduce(f func(T, T) T) Option[T]
func (s Stream[T]) Find(p func(T) bool) Option[T]
func (s Stream[T]) Exists(p func(T) bool) bool
func (s Stream[T]) ForAll(p func(T) bool) bool
func (s Stream[T]) Count() int  // Warning: infinite streams!
func (s Stream[T]) MkString(sep string) string

// Pattern matching support
type StreamCons[T any] struct{}
func (c StreamCons[T]) Unapply(s Stream[T]) Option[Tuple2[T, Stream[T]]]
```

### Example Usage

```gala
// Fibonacci sequence (infinite)
val fibs = Unfold[int, Tuple2[int, int]](
    Tuple2(0, 1),
    (state Tuple2[int, int]) => Some(Tuple2(state._1, Tuple2(state._2, state._1 + state._2)))
)

// First 10 Fibonacci numbers
val first10 = fibs.Take(10).ToArray()

// Prime sieve (infinite)
func sieve(s Stream[int]) Stream[int] = match s.Head() {
    case Some(p) => Cons(p, () => sieve(s.Tail().Filter((n int) => n % p != 0)))
    case None() => Empty[int]()
}
val primes = sieve(From(2))
```

### BUILD.bazel

```starlark
load("@rules_go//go:def.bzl", "go_library")
load("//:gala.bzl", "gala_bootstrap_transpile", "gala_go_test")

exports_files(["stream.gala"])

filegroup(
    name = "gala_sources",
    srcs = glob(["*.gala"], exclude = ["*_test.gala"]),
    visibility = ["//visibility:public"],
)

gala_bootstrap_transpile(
    name = "stream_go",
    src = "stream.gala",
    out = "stream.gen.go",
)

go_library(
    name = "stream",
    srcs = ["stream.gen.go"],
    importpath = "martianoff/gala/stream",
    visibility = ["//visibility:public"],
    deps = [
        "//std",
        "//collection_immutable",
    ],
)

gala_go_test(
    name = "stream_test",
    srcs = ["stream_test.gala"],
    deps = [
        ":stream",
        "//collection_immutable",
    ],
)
```

---

## 2. Package: `string_utils`

**Purpose**: Rich string operations beyond Go's standard library.

### Design Decision: Store `Array[rune]`

The `Str` type stores `Array[rune]` internally (not `string`) for optimal performance:

| Aspect | Store `string` | Store `Array[rune]` (chosen) |
|--------|---------------|------------------------------|
| `Length()` | O(n) - count runes | **O(1)** - array length |
| Rune operations | O(n) conversion each time | **Direct access** |
| Chained ops | Multiple conversions | **Single representation** |
| Go interop | Direct | O(n) via `ToString()` |

Most operations (Map, Filter, Reverse, etc.) work on runes, so storing them directly enables delegation to `Array[T]` methods.

### API Design

```gala
package string_utils

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

// Immutable String wrapper storing runes for efficient character operations
type Str struct {
    runes Array[rune]
}

// Constructors
func S(s string) Str                    // Create from Go string

// Basic operations - O(1)
func (s Str) Length() int               // Number of runes
func (s Str) IsEmpty() bool
func (s Str) NonEmpty() bool
func (s Str) CharAt(index int) Option[rune]

// Slicing
func (s Str) Substring(start int, end int) Str
func (s Str) Take(n int) Str
func (s Str) TakeRight(n int) Str
func (s Str) Drop(n int) Str
func (s Str) DropRight(n int) Str

// Case transformations
func (s Str) ToUpper() Str
func (s Str) ToLower() Str
func (s Str) Capitalize() Str
func (s Str) Uncapitalize() Str

// Trimming
func (s Str) Trim() Str
func (s Str) TrimLeft() Str
func (s Str) TrimRight() Str
func (s Str) TrimPrefix(prefix string) Str
func (s Str) TrimSuffix(suffix string) Str

// Replacement
func (s Str) Replace(old string, new string) Str
func (s Str) ReplaceAll(old string, new string) Str

// Other transformations
func (s Str) Reverse() Str              // Delegates to Array.Reverse()
func (s Str) Repeat(n int) Str
func (s Str) PadLeft(length int, pad rune) Str
func (s Str) PadRight(length int, pad rune) Str
func (s Str) Center(length int, pad rune) Str

// Splitting and joining
func (s Str) Split(sep string) Array[Str]
func (s Str) SplitAt(index int) Tuple[Str, Str]
func (s Str) Lines() Array[Str]
func (s Str) Words() Array[Str]
func Join(strs Array[Str], sep string) Str

// Concatenation
func (s Str) Concat(other Str) Str
func (s Str) Plus(other Str) Str        // Alias for Concat

// Predicates
func (s Str) Contains(substr string) bool
func (s Str) ContainsAny(chars string) bool
func (s Str) StartsWith(prefix string) bool
func (s Str) EndsWith(suffix string) bool
func (s Str) IsAlpha() bool             // s.runes.ForAll(unicode.IsLetter)
func (s Str) IsNumeric() bool           // s.runes.ForAll(unicode.IsDigit)
func (s Str) IsAlphanumeric() bool
func (s Str) IsWhitespace() bool
func (s Str) IsUpper() bool
func (s Str) IsLower() bool

// Search
func (s Str) IndexOf(substr string) Option[int]
func (s Str) LastIndexOf(substr string) Option[int]
func (s Str) IndexOfChar(target rune) Option[int]
func (s Str) Count(substr string) int

// Comparison
func (s Str) Equals(other Str) bool
func (s Str) EqualsIgnoreCase(other Str) bool
func (s Str) Compare(other Str) int

// Functional operations - delegate to Array[rune]
func (s Str) Map(f func(rune) rune) Str           // s.runes.Map(f)
func (s Str) Filter(p func(rune) bool) Str        // s.runes.Filter(p)
func (s Str) FilterNot(p func(rune) bool) Str
func (s Str) ForEach(f func(rune))
func (s Str) Fold[U any](zero U, f func(U, rune) U) U
func (s Str) Exists(p func(rune) bool) bool
func (s Str) ForAll(p func(rune) bool) bool
func (s Str) Find(p func(rune) bool) Option[rune]
func (s Str) ZipWithIndex() Array[Tuple[rune, int]]

// Conversion
func (s Str) ToChars() Array[rune]      // Returns internal array
func (s Str) ToString() string          // O(n) conversion

// Pattern matching extractors
type NonEmptyStr struct{}
func (n NonEmptyStr) Unapply(s Str) Option[Tuple[rune, Str]]  // (head, tail)

type EmptyStr struct{}
func (e EmptyStr) Unapply(s Str) Option[bool]
```

### Example Usage

```gala
// Basic usage
val text = S("  Hello, World!  ")
val processed = text.Trim().ToLower().ReplaceAll(",", "").ReplaceAll("!", "")
// processed.ToString() == "hello world"

// Functional operations delegate to Array methods
val vowels = S("hello").Filter((r rune) => r == rune(97) || r == rune(101) || r == rune(105) || r == rune(111) || r == rune(117))
// vowels.ToString() == "eo"

// Predicates use ForAll
val allLetters = S("hello").IsAlpha()  // true - uses runes.ForAll(unicode.IsLetter)

// Pattern matching with extractors
match S("hello") {
    case NonEmptyStr(head, tail) => fmt.Printf("Head: %c, Tail: %s", head, tail.ToString())
    case EmptyStr(_) => fmt.Println("Empty string")
}

// Chained operations are efficient (single Array[rune] representation)
val result = S("hello world")
    .Map((r rune) => unicode.ToUpper(r))
    .Filter((r rune) => r != rune(32))
    .Reverse()
// result.ToString() == "DLROWOLLEH"
```

---

## 3. Package: `time_utils`

**Purpose**: Immutable date/time types with functional operations.

### API Design

```gala
package time_utils

import (
    "time"
    . "martianoff/gala/std"
)

// Immutable instant in time
type Instant struct {
    nanos int64  // nanoseconds since Unix epoch
}

// Duration type
type Duration struct {
    nanos int64
}

// Instant constructors
func Now() Instant
func FromUnixSeconds(secs int64) Instant
func FromUnixMillis(millis int64) Instant
func FromUnixNanos(nanos int64) Instant
func FromGoTime(t time.Time) Instant
func Parse(layout string, value string) Option[Instant]
func ParseISO(value string) Option[Instant]

// Instant operations
func (i Instant) Plus(d Duration) Instant
func (i Instant) Minus(d Duration) Instant
func (i Instant) Until(other Instant) Duration
func (i Instant) IsBefore(other Instant) bool
func (i Instant) IsAfter(other Instant) bool
func (i Instant) Equals(other Instant) bool

// Instant accessors
func (i Instant) UnixSeconds() int64
func (i Instant) UnixMillis() int64
func (i Instant) UnixNanos() int64
func (i Instant) ToGoTime() time.Time
func (i Instant) Format(layout string) string
func (i Instant) FormatISO() string

// Date components (in UTC)
func (i Instant) Year() int
func (i Instant) Month() int
func (i Instant) Day() int
func (i Instant) Hour() int
func (i Instant) Minute() int
func (i Instant) Second() int
func (i Instant) Weekday() int

// Duration constructors
func Nanoseconds(n int64) Duration
func Microseconds(n int64) Duration
func Milliseconds(n int64) Duration
func Seconds(n int64) Duration
func Minutes(n int64) Duration
func Hours(n int64) Duration
func Days(n int64) Duration
func Between(start Instant, end Instant) Duration

// Duration operations
func (d Duration) Plus(other Duration) Duration
func (d Duration) Minus(other Duration) Duration
func (d Duration) Multiply(factor int64) Duration
func (d Duration) Divide(divisor int64) Duration
func (d Duration) Abs() Duration
func (d Duration) Negate() Duration
func (d Duration) IsZero() bool
func (d Duration) IsNegative() bool
func (d Duration) IsPositive() bool

// Duration accessors
func (d Duration) ToNanos() int64
func (d Duration) ToMicros() int64
func (d Duration) ToMillis() int64
func (d Duration) ToSeconds() int64
func (d Duration) ToMinutes() int64
func (d Duration) ToHours() int64
func (d Duration) ToDays() int64
func (d Duration) ToGoDuration() time.Duration

// Duration formatting
func (d Duration) String() string  // "2h30m15s"

// Comparison
func (d Duration) Compare(other Duration) int

// Timer utilities
func Sleep(d Duration)
func After(d Duration) Instant
```

### Example Usage

```gala
val now = Now()
val tomorrow = now.Plus(Days(1))
val diff = now.Until(tomorrow)

fmt.Println(diff.ToHours())  // 24

val parsed = ParseISO("2024-01-15T10:30:00Z")
match parsed {
    case Some(instant) => fmt.Println(instant.Format("Jan 2, 2006"))
    case None() => fmt.Println("Invalid date")
}

// Timing operations
val start = Now()
// ... work ...
val elapsed = start.Until(Now())
fmt.Printf("Took %s\n", elapsed.String())
```

---

## 4. Package: `json`

**Purpose**: Type-safe JSON encoding/decoding with functional error handling.

### API Design

```gala
package json

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

// JSON value types
type Json interface {
    IsNull() bool
    IsObject() bool
    IsArray() bool
    IsString() bool
    IsNumber() bool
    IsBool() bool
}

type JsonNull struct{}
type JsonBool struct { value bool }
type JsonNumber struct { value float64 }
type JsonString struct { value string }
type JsonArray struct { values Array[Json] }
type JsonObject struct { fields HashMap[string, Json] }

// Constructors
func Null() JsonNull
func Bool(b bool) JsonBool
func Number(n float64) JsonNumber
func NumberInt(n int) JsonNumber
func String(s string) JsonString
func Arr(values ...Json) JsonArray
func Obj(fields ...Tuple2[string, Json]) JsonObject

// Parsing
func Parse(input string) Either[JsonError, Json]
func ParseBytes(input []byte) Either[JsonError, Json]

// Navigation (cursor-style)
func (j Json) Field(name string) Option[Json]
func (j Json) Index(i int) Option[Json]
func (j Json) At(path ...string) Option[Json]

// Extraction
func (j Json) AsString() Option[string]
func (j Json) AsInt() Option[int]
func (j Json) AsFloat() Option[float64]
func (j Json) AsBool() Option[bool]
func (j Json) AsArray() Option[Array[Json]]
func (j Json) AsObject() Option[HashMap[string, Json]]

// Encoding
func (j Json) Encode() string
func (j Json) EncodePretty() string
func (j Json) EncodeBytes() []byte

// Decoder typeclass pattern
type Decoder[T any] interface {
    Decode(j Json) Either[JsonError, T]
}

// Encoder typeclass pattern
type Encoder[T any] interface {
    Encode(value T) Json
}

// Built-in decoders
var StringDecoder Decoder[string]
var IntDecoder Decoder[int]
var FloatDecoder Decoder[float64]
var BoolDecoder Decoder[bool]
func ArrayDecoder[T any](elemDecoder Decoder[T]) Decoder[Array[T]]
func OptionDecoder[T any](elemDecoder Decoder[T]) Decoder[Option[T]]

// Decode helper
func Decode[T any](input string, decoder Decoder[T]) Either[JsonError, T]

// Error types
type JsonError interface {
    Message() string
    Path() string
}

type ParseError struct { message string; position int }
type TypeError struct { expected string; actual string; path string }
type MissingFieldError struct { field string; path string }

// Pattern matching extractors
type JString struct{}
func (e JString) Unapply(j Json) Option[string]

type JNumber struct{}
func (e JNumber) Unapply(j Json) Option[float64]

type JBool struct{}
func (e JBool) Unapply(j Json) Option[bool]

type JArray struct{}
func (e JArray) Unapply(j Json) Option[Array[Json]]

type JObject struct{}
func (e JObject) Unapply(j Json) Option[HashMap[string, Json]]
```

### Example Usage

```gala
// Parsing
val jsonStr = `{"name": "Alice", "age": 30, "active": true}`
val parsed = Parse(jsonStr)

match parsed {
    case Right(json) => {
        val name = json.Field("name").FlatMap((j Json) => j.AsString())
        val age = json.Field("age").FlatMap((j Json) => j.AsInt())
        fmt.Printf("Name: %s, Age: %d\n", name.GetOrElse("unknown"), age.GetOrElse(0))
    }
    case Left(err) => fmt.Println("Parse error:", err.Message())
}

// Building JSON
val person = Obj(
    Tuple2("name", String("Bob")),
    Tuple2("age", Number(25)),
    Tuple2("tags", Arr(String("dev"), String("go")))
)
fmt.Println(person.EncodePretty())

// Pattern matching on JSON
match someJson {
    case JObject(obj) => {
        match obj.Get("type") {
            case Some(JString("user")) => handleUser(obj)
            case Some(JString("admin")) => handleAdmin(obj)
            case _ => handleUnknown(obj)
        }
    }
    case JArray(items) => items.ForEach(processItem)
    case _ => fmt.Println("Unexpected JSON type")
}
```

---

## 5. Package: `parser`

**Purpose**: Parser combinator library for building parsers from small pieces.

### API Design

```gala
package parser

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

// Parser input
type Input struct {
    source string
    offset int
}

// Parse result
type ParseResult[T any] = Either[ParseError, Tuple2[T, Input]]

// Core parser type
type Parser[T any] struct {
    run func(Input) ParseResult[T]
}

// Parse error
type ParseError struct {
    message string
    position int
    expected Array[string]
}

// Run parser
func (p Parser[T]) Parse(input string) Either[ParseError, T]
func (p Parser[T]) ParseAll(input string) Either[ParseError, T]  // Must consume all input

// Basic parsers
func Pure[T any](value T) Parser[T]
func Fail[T any](message string) Parser[T]
func Char(c rune) Parser[rune]
func CharIn(chars string) Parser[rune]
func CharNotIn(chars string) Parser[rune]
func String(s string) Parser[string]
func Regex(pattern string) Parser[string]
func AnyChar() Parser[rune]
func Digit() Parser[rune]
func Letter() Parser[rune]
func Whitespace() Parser[rune]
func EOF() Parser[Unit]

// Combinators
func (p Parser[T]) Map[U any](f func(T) U) Parser[U]
func (p Parser[T]) FlatMap[U any](f func(T) Parser[U]) Parser[U]
func (p Parser[T]) Filter(pred func(T) bool, msg string) Parser[T]
func (p Parser[T]) Or(other Parser[T]) Parser[T]
func (p Parser[T]) AndThen[U any](other Parser[U]) Parser[Tuple2[T, U]]
func (p Parser[T]) SkipLeft[U any](other Parser[U]) Parser[U]
func (p Parser[T]) SkipRight[U any](other Parser[U]) Parser[T]
func (p Parser[T]) Optional() Parser[Option[T]]
func (p Parser[T]) Many() Parser[Array[T]]
func (p Parser[T]) Many1() Parser[Array[T]]
func (p Parser[T]) SepBy[S any](sep Parser[S]) Parser[Array[T]]
func (p Parser[T]) SepBy1[S any](sep Parser[S]) Parser[Array[T]]
func (p Parser[T]) Between[L any, R any](left Parser[L], right Parser[R]) Parser[T]
func (p Parser[T]) Surrounded[S any](surround Parser[S]) Parser[T]
func (p Parser[T]) Label(name string) Parser[T]
func (p Parser[T]) Attempt() Parser[T]  // Backtracking

// Sequencing helpers
func Seq2[A any, B any](p1 Parser[A], p2 Parser[B]) Parser[Tuple2[A, B]]
func Seq3[A any, B any, C any](p1 Parser[A], p2 Parser[B], p3 Parser[C]) Parser[Tuple3[A, B, C]]

// Choice helpers
func Choice[T any](parsers ...Parser[T]) Parser[T]

// Repetition helpers
func Count[T any](n int, p Parser[T]) Parser[Array[T]]
func ManyTill[T any, E any](p Parser[T], end Parser[E]) Parser[Array[T]]

// Lexeme helpers (skip trailing whitespace)
func Lexeme[T any](p Parser[T]) Parser[T]
func Symbol(s string) Parser[string]

// Common parsers
func Integer() Parser[int]
func Float() Parser[float64]
func QuotedString() Parser[string]
func Identifier() Parser[string]
func Spaces() Parser[string]
func Newline() Parser[rune]
```

### Example Usage

```gala
// Simple expression parser
type Expr interface{}
type Num struct { value int }
type Add struct { left Expr; right Expr }
type Mul struct { left Expr; right Expr }

func number() Parser[Expr] = Integer().Map((n int) => Num(value = n) as Expr)

func factor() Parser[Expr] = Choice(
    number(),
    Char('(').SkipLeft(expr()).SkipRight(Char(')'))
)

func term() Parser[Expr] = factor().FlatMap((left Expr) =>
    Char('*').SkipLeft(term()).Map((right Expr) => Mul(left = left, right = right) as Expr)
    .Or(Pure(left))
)

func expr() Parser[Expr] = term().FlatMap((left Expr) =>
    Char('+').SkipLeft(expr()).Map((right Expr) => Add(left = left, right = right) as Expr)
    .Or(Pure(left))
)

// Parse "1+2*3"
val result = expr().Parse("1+2*3")
// result == Right(Add(Num(1), Mul(Num(2), Num(3))))

// JSON-like parser
func jsonValue() Parser[Json] = Choice(
    String("null").Map((_) => Null() as Json),
    String("true").Map((_) => Bool(true) as Json),
    String("false").Map((_) => Bool(false) as Json),
    Float().Map((n float64) => Number(n) as Json),
    QuotedString().Map((s string) => String(s) as Json),
    jsonArray(),
    jsonObject()
)
```

---

## 6. Package: `io`

**Purpose**: IO monad for pure functional programming with side effects.

### API Design

```gala
package io

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

// IO monad - describes a computation that may have side effects
type IO[T any] struct {
    unsafeRun func() T
}

// Constructors
func Pure[T any](value T) IO[T]
func Suspend[T any](thunk func() T) IO[T]
func Fail[T any](err error) IO[T]
func FromTry[T any](t Try[T]) IO[T]
func FromOption[T any](o Option[T], ifNone func() error) IO[T]

// Running (unsafe - breaks referential transparency)
func (io IO[T]) Run() T
func (io IO[T]) RunSafe() Try[T]

// Combinators
func (io IO[T]) Map[U any](f func(T) U) IO[U]
func (io IO[T]) FlatMap[U any](f func(T) IO[U]) IO[U]
func (io IO[T]) AndThen[U any](next IO[U]) IO[U]
func (io IO[T]) HandleError(handler func(error) IO[T]) IO[T]
func (io IO[T]) Attempt() IO[Either[error, T]]
func (io IO[T]) Forever() IO[Nothing]
func (io IO[T]) Repeat(n int) IO[Array[T]]
func (io IO[T]) Retry(attempts int) IO[T]
func (io IO[T]) Timeout(d Duration) IO[T]
func (io IO[T]) OnCancel(cleanup IO[Unit]) IO[T]

// Sequencing
func Sequence[T any](ios Array[IO[T]]) IO[Array[T]]
func Traverse[T any, U any](arr Array[T], f func(T) IO[U]) IO[Array[U]]
func ParSequence[T any](ios Array[IO[T]]) IO[Array[T]]  // Parallel execution
func Race[T any](ios Array[IO[T]]) IO[T]  // First to complete

// Resource management (bracket pattern)
func Bracket[R any, A any](
    acquire IO[R],
    use func(R) IO[A],
    release func(R) IO[Unit]
) IO[A]

// Console IO
func PrintLine(s string) IO[Unit]
func Print(s string) IO[Unit]
func ReadLine() IO[string]

// File IO
func ReadFile(path string) IO[string]
func WriteFile(path string, content string) IO[Unit]
func AppendFile(path string, content string) IO[Unit]
func FileExists(path string) IO[bool]
func DeleteFile(path string) IO[Unit]
func ListDir(path string) IO[Array[string]]

// Environment
func GetEnv(key string) IO[Option[string]]
func SetEnv(key string, value string) IO[Unit]

// Random
func RandomInt(min int, max int) IO[int]
func RandomFloat() IO[float64]
func RandomBool() IO[bool]

// Time
func CurrentTime() IO[Instant]
func SleepIO(d Duration) IO[Unit]

// Nothing type for non-terminating computations
type Nothing struct{}
type Unit struct{}
func UnitValue() Unit
```

### Example Usage

```gala
// Pure program description
func program() IO[Unit] =
    PrintLine("What is your name?").AndThen(
        ReadLine().FlatMap((name string) =>
            PrintLine("Hello, " + name + "!")
        )
    )

// File processing with resource safety
func processFile(path string) IO[int] = Bracket(
    ReadFile(path),                           // acquire
    (content string) => Pure(len(content)),   // use
    (_) => Pure(UnitValue())                  // release (no cleanup needed for string)
)

// Retry with error handling
func fetchWithRetry(url string) IO[string] =
    fetch(url).Retry(3).HandleError((err error) =>
        Pure("fallback value")
    )

// Parallel operations
func fetchAll(urls Array[string]) IO[Array[string]] =
    ParSequence(urls.Map((url string) => fetch(url)))

// Main entry point
func main() {
    program().Run()  // Actually execute the IO
}
```

---

## Implementation Order

### Phase 1: Foundation (Week 1-2)

1. **`stream`** - Foundation for lazy evaluation patterns
   - Enables lazy collection operations
   - Useful for other packages (e.g., parser backtracking)

2. **`string_utils`** - High utility, low complexity
   - Immediately useful for all GALA programs
   - Good test case for library patterns

### Phase 2: Practical Utilities (Week 3-4)

3. **`time_utils`** - Essential for real applications
   - Common need in most programs
   - Well-defined scope

4. **`json`** - Critical for modern applications
   - Web services, configuration
   - Demonstrates GALA's type safety

### Phase 3: Advanced Features (Week 5-6)

5. **`parser`** - Powerful abstraction
   - Demonstrates GALA's functional capabilities
   - Useful for DSLs, configuration parsing

6. **`io`** - Pure FP foundation
   - Most advanced concept
   - Enables fully pure programs

---

## Testing Strategy

Each package should include:

1. **Unit tests** - Test individual functions
2. **Property-based tests** - Test invariants (where applicable)
3. **Integration tests** - Test composition of operations
4. **Performance benchmarks** - Compare with Go stdlib equivalents
5. **Example programs** in `examples/` directory

### Test File Template

```gala
package main

import (
    . "martianoff/gala/test"
    . "martianoff/gala/stream"
)

func TestStreamTake(t T) T {
    val s = From(1).Take(5).ToArray()
    var t1 = Eq[int](t, s.Length(), 5)
    var t2 = Eq[int](t1, s.Get(0).GetOrElse(0), 1)
    return Eq[int](t2, s.Get(4).GetOrElse(0), 5)
}

func TestStreamMap(t T) T {
    val doubled = Of(1, 2, 3).Map((x int) => x * 2).ToArray()
    var t1 = Eq[int](t, doubled.Get(0).GetOrElse(0), 2)
    var t2 = Eq[int](t1, doubled.Get(1).GetOrElse(0), 4)
    return Eq[int](t2, doubled.Get(2).GetOrElse(0), 6)
}
```

---

## Documentation Updates

After implementation, update:

1. **docs/GALA.MD** - Add section for each new package
2. **docs/EXAMPLES.MD** - Add usage examples
3. **README.md** - Update standard library overview

---

## Checklist Per Package

### `stream` - ✅ COMPLETE
- [x] Create directory structure
- [x] Implement core types and functions
- [x] Write BUILD.bazel
- [x] Add to root BUILD.bazel filegroup
- [x] Write comprehensive tests (40+ test cases)
- [ ] Add example program in `examples/`
- [ ] Update documentation
- [x] Run `bazel build //...` and `bazel test //...`
- [x] Run `bazel run //:gazelle`

### `string_utils` - ✅ COMPLETE
- [x] Create directory structure
- [x] Implement core types and functions
- [x] Write BUILD.bazel
- [x] Add to root BUILD.bazel filegroup
- [x] Write comprehensive tests (39 test cases)
- [ ] Add example program in `examples/`
- [x] Update documentation
- [x] Run `bazel build //...` and `bazel test //...`
- [x] Run `bazel run //:gazelle`

**Implementation Notes**:

1. **Transpiler bug discovered and fixed**:
   - Bug: Tuple element types from `Array[T].Get()` were incorrectly inferred as `Array[T]` instead of `T`
   - Fix: Updated `type_inference.go` to check for arguments before treating `.Get()` as Immutable unwrap
   - Fix: Updated `transformer.go` to try dot imports when resolving simple names from qualified types

2. **Design decision: Store `Array[rune]` instead of `string`**:
   - `Length()` is O(1) instead of O(n)
   - Functional operations (Map, Filter, Fold, etc.) delegate directly to `Array[T]` methods
   - Predicate methods (IsAlpha, IsNumeric, etc.) are one-liners using `ForAll`
   - Chained operations avoid repeated string-to-runes conversions
   - Code reduced from ~590 lines to ~360 lines

3. **GALA best practices applied**:
   - Expression-bodied functions where possible
   - Direct delegation to collection methods instead of manual loops
   - Pattern matching extractors (`NonEmptyStr`, `EmptyStr`) for functional decomposition

### `time_utils` - ✅ COMPLETE
- [x] Create directory structure
- [x] Implement core types and functions
- [x] Write BUILD.bazel
- [x] Add to root BUILD.bazel filegroup
- [x] Write comprehensive tests (56 test cases)
- [ ] Add example program in `examples/`
- [ ] Update documentation
- [x] Run `bazel build //...` and `bazel test //...`
- [x] Run `bazel run //:gazelle`

**Implementation Notes**:

1. **Transpiler bug discovered and fixed**:
   - Bug: When resolving type names like `time.Duration`, the transpiler would extract just `Duration` and match it against GALA-defined structs
   - This caused `time.Duration(d.nanos)` to be incorrectly transpiled as struct construction `time.Duration{nanos: ...}` instead of type conversion
   - Fix: In `resolveTypeName()` in `transformer.go`, added check for external package prefixes to prevent resolving simple names to GALA types when the qualified name comes from an external Go package

2. **Multi-value return workaround**:
   - GALA's `val result, err = time.Parse(...)` syntax is not fully working for Go functions returning multiple values
   - Workaround: Use separate var declarations and assignment: `var result time.Time; var err error; result, err = time.Parse(...)`
   - This limitation should be addressed in a future transpiler update

3. **Type inference for literals**:
   - When using `0` in a context expecting `int64`, explicit conversion is needed: `int64(0)`
   - Type inference from struct field types does not automatically promote integer literals

4. **Pattern matching with Tuple6**:
   - Complex tuple extractors (Tuple3, Tuple6) work via explicit Unapply() calls
   - Direct pattern matching syntax `case InstantComponents(a, b, c, d, e, f) =>` has limitations
   - Tests use explicit extractor instance: `val extractor InstantComponents = InstantComponents{}; extractor.Unapply(i).Get()`

### `json` - ⏳ PENDING
- [ ] Create directory structure
- [ ] Implement core types and functions
- [ ] Write BUILD.bazel
- [ ] Add to root BUILD.bazel filegroup
- [ ] Write comprehensive tests
- [ ] Add example program in `examples/`
- [ ] Update documentation
- [ ] Run `bazel build //...` and `bazel test //...`
- [ ] Run `bazel run //:gazelle`

### `parser` - ⏳ PENDING
- [ ] Create directory structure
- [ ] Implement core types and functions
- [ ] Write BUILD.bazel
- [ ] Add to root BUILD.bazel filegroup
- [ ] Write comprehensive tests
- [ ] Add example program in `examples/`
- [ ] Update documentation
- [ ] Run `bazel build //...` and `bazel test //...`
- [ ] Run `bazel run //:gazelle`

### `io` - ⏳ PENDING
- [ ] Create directory structure
- [ ] Implement core types and functions
- [ ] Write BUILD.bazel
- [ ] Add to root BUILD.bazel filegroup
- [ ] Write comprehensive tests
- [ ] Add example program in `examples/`
- [ ] Update documentation
- [ ] Run `bazel build //...` and `bazel test //...`
- [ ] Run `bazel run //:gazelle`
