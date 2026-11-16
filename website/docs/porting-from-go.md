---
layout: default
title: "From Go to GALA — A Porting Guide for Go Developers"
description: "Every Go construct a Go developer reaches for, mapped to its GALA form or to go_interop, Codec[T] or a hand-written Go file, with the GALA-Exxxx error the Go form gets: switch, (T, error), defer, panic, slices and maps, struct tags, const, goroutines, channels and select. Every listing is a tested file."
keywords: "port go to gala, go to gala translation, gala for go developers, gala switch match, gala defer use, gala struct tags codec, gala goroutines future, gala slices go_interop, gala const, gala error codes"
permalink: /docs/porting-from-go/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Porting from Go</p>

{% raw %}

# From Go to GALA: a Porting Guide

You know Go and are porting Go code to GALA, or writing GALA next to it. This page takes every Go construct you are likely to type and gives its GALA form. Where GALA has no form, it says so and names the way out: a `go_interop` helper, a `Codec[T]`, or a hand-written `.go` file in the same package. For each Go form it also gives what the compiler says if you write it anyway.

The tables are the reference. The [worked sections](#worked-sections) after them cover the cases that need more than a row: `switch`, `(T, error)`, `defer`, `panic`, slices and maps, struct tags, exported values, and goroutines with channels. Every listing in those sections, except the one that shows a compile error, is quoted from [`examples/porting_from_go`](https://github.com/martianoff/gala/tree/master/examples/porting_from_go), which Bazel builds and runs, and a doc test fails when a listing stops matching its file. The error codes in the tables were checked by compiling each Go form with `gala build`; the tables themselves are not under test, so `gala build --json` on your own code is the final word.

To add GALA to an existing Go module first, read [Add GALA to an Existing Go Repository](/docs/add-gala-to-go-repo/). For the full rules of mixing the two languages, see the [Go Interop Guide](/docs/go-interop/).

This page adapts the [Go to GALA translation roster](https://github.com/time-tools/timeful/blob/6726aa3744a8b03688ac316e81bebac856d2866f/docs/gala-translation.md) that @deemp wrote while porting a Go server; thanks to @deemp. That roster was pinned to GALA 0.81.0. Every verdict here was checked again against the current compiler, and many have changed since.

## Table of Contents

1. [How to read the tables](#how-to-read-the-tables)
2. [Declarations and types](#declarations-and-types)
3. [Functions and signatures](#functions-and-signatures)
4. [Control flow](#control-flow)
5. [Builtins and expressions](#builtins-and-expressions)
6. [Slices and maps](#slices-and-maps)
7. [Errors, panic and defer](#errors-panic-and-defer)
8. [Concurrency](#concurrency)
9. [Packages and files](#packages-and-files)
10. [Worked sections](#worked-sections)
    - [`switch`: match or an if-chain](#switch-match-or-an-if-chain)
    - [`(T, error)`: Try, or Go's result list](#t-error-try-or-gos-result-list)
    - [`panic` and `recover`: Try first](#panic-and-recover-try-first)
    - [`defer`: `use` and the resource package](#defer-use-and-the-resource-package)
    - [Slices and maps: go_interop at the boundary](#slices-and-maps-go_interop-at-the-boundary)
    - [Struct tags: a codec, or a Go file](#struct-tags-a-codec-or-a-go-file)
    - [`const`, exported values, and methods on scalar types](#const-exported-values-and-methods-on-scalar-types)
    - [Goroutines, channels and `select`](#goroutines-channels-and-select)
11. [What stays in Go](#what-stays-in-go)

---

## How to read the tables

Each row has three columns:

- **Go**: the construct as you would write it in Go.
- **GALA**: the GALA form. "Same" means the Go spelling works as it is. "None" means GALA has no form for it, and the cell says what to do instead.
- **If you write the Go form**: what the compiler does with the Go spelling in a `.gala` file. A `GALA-Exxxx` code links to its page, and `gala explain GALA-Exxxx` prints the same page offline. "Parse error" is a syntax error without a code. "Works" means the Go spelling is accepted.

Most Go statements and expressions work as they are: `for` loops, `if`/`else`, `x := v`, `i++`, `&x`, method calls, `fmt`, and the rest of the Go standard library. The tables list them so that you can stop wondering, but most rows are about the places where GALA differs.

## Declarations and types

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `x := 5` | Same, but the binding is immutable, as with `val`; `val x = 5` is the usual GALA form, and `var x = 5` one that is reassigned | Works; reassigning `x` is an error |
| `var x = 5`, `var x int` | Same: `var` declares a variable that can be reassigned | Works |
| `n, err := strconv.Atoi(s)` | Same, and gives Go's raw values; in GALA code prefer the `Try`: `val n = strconv.Atoi(s)` ([below](#t-error-try-or-gos-result-list)) | Works |
| `v, ok := m[k]` on a Go map | Same, or `val v, ok = m[k]`; `go_interop.OptionFromMap(m, k)` gives an `Option` | Works |
| `a, b = b, a` | Same | Works |
| `const N = 10` | `val N = 10` ([below](#const-exported-values-and-methods-on-scalar-types)); a constant a Go API must keep stays in a `.go` file | [GALA-E0121](/docs/errors/gala-e0121/) |
| `iota` | A `sealed type` for an enumeration; a Go `const` block in a `.go` file when the numbers matter | [GALA-E0121](/docs/errors/gala-e0121/) |
| `type Point struct { X int }` | Same block form, or the shorthand `struct Point(X int)`. Fields are immutable unless declared `var` | Works |
| `Point{X: 1}` | Same, or GALA's `Point(X = 1)` | Works |
| `type Celsius float64` with methods | `opaque type Celsius float64` ([below](#const-exported-values-and-methods-on-scalar-types)) | [GALA-E0048](/docs/errors/gala-e0048/) on the method: `type X Y` is an alias in GALA |
| `type Name = string` | `type Name string` (no `=`; it is an alias) | Parse error |
| `type Number interface { ~int \| ~float64 }` | None in GALA syntax: declare the constraint in a `.go` file, or use one such as `cmp.Ordered` | [GALA-E0127](/docs/errors/gala-e0127/) |
| `type RW interface { io.Reader; io.Writer }` | List the methods; embedded interfaces have no GALA form | [GALA-E0126](/docs/errors/gala-e0126/) |
| `interface{}` | `any` | [GALA-E0125](/docs/errors/gala-e0125/) |
| struct tags, `` Name string `json:"name"` `` | `Codec[T]` keys, or the type in a `.go` file ([below](#struct-tags-a-codec-or-a-go-file)) | [GALA-E0085](/docs/errors/gala-e0085/) |
| embedded fields, `struct { Base; Name string }` | Name the field (`Base Base`) and call through it, or keep the type in a `.go` file | [GALA-E0126](/docs/errors/gala-e0126/) |
| `struct { X int }` (anonymous struct type) | A named struct; a Go value of such a type can be used but not named | Parse error; [GALA-E0090](/docs/errors/gala-e0090/) where the generated Go would have to write the type |
| `map[string]struct{}` (a set) | `HashSet[string]` from `collection_immutable` | Parse error |
| `[16]byte` (fixed-size array) | None: a slice, or the type in a `.go` file | [GALA-E0125](/docs/errors/gala-e0125/) |
| `x.(T)`, `v, ok := x.(T)` | `x match { case v: T => ... case _ => ... }` | [GALA-E0124](/docs/errors/gala-e0124/) |
| `var p *T` / `*p = 3` | Same; a pointer GALA creates is a `Ptr[T]`, never nil ([GALA-E0080](/docs/errors/gala-e0080/)) | Works |
| `val sb = strings.Builder{}` then `sb.WriteString(...)` | `var sb strings.Builder` (also `sync.Mutex`, `bytes.Buffer`) | [GALA-E0053](/docs/errors/gala-e0053/) |
| a name that is a Go keyword, `default := 1` | Another name | [GALA-E0055](/docs/errors/gala-e0055/) |

## Functions and signatures

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `func f(a, b int) int { return a + b }` | Same, but each parameter has its own type: `func f(a int, b int) int = a + b` | [GALA-E0034](/docs/errors/gala-e0034/) for `a, b int` |
| `return x` at the end of a block | Same; the last expression of a block is its value, so `x` alone also works | Works |
| `func f() (int, error)` | Same signature; the body gives a `Try[int]` ([below](#t-error-try-or-gos-result-list)) | Works |
| `return 0, err` / `return n, nil` | `Failure(err)` / `Success(n)` | [GALA-E0132](/docs/errors/gala-e0132/) |
| named results, `func f() (n int, err error)` | Unnamed results | [GALA-E0130](/docs/errors/gala-e0130/) |
| `func(x int) int { return x + 1 }` | A lambda: `(x int) => x + 1` | [GALA-E0117](/docs/errors/gala-e0117/) |
| `Cmd{Run: func() string { ... }}` | `Cmd(Run = () => ...)`: the lambda takes its types from the field | [GALA-E0117](/docs/errors/gala-e0117/) |
| a named `func` inside a function | `val helper = (x int) => ...`; for recursion, `var fact func(int) int` then assign | [GALA-E0052](/docs/errors/gala-e0052/) |
| `func sum(xs ...int)`, `f(xs...)` | Same | Works |
| `func f(_ int)` | Same | Works |
| `func (c *Counter) Inc()` | Same | Works |
| `func Map[T, U any](...)` | Each type parameter with its own constraint: `[T any, U any]`; call with or without type arguments | [GALA-E0131](/docs/errors/gala-e0131/) |
| `f := strings.ToUpper`, `w := sb.WriteString` | Same (function and method values) | Works |
| `func init()`, `func main()` | Same | Works |
| `http.HandlerFunc(func(w, r) {...})` | `http.HandlerFunc((w, r) => { ... })` | [GALA-E0117](/docs/errors/gala-e0117/) |

## Control flow

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `if cond { ... } else { ... }` | Same | Works |
| `if v, err := f(); err != nil` | `f() match { case Success(v) => ... case Failure(e) => ... }`, or `.GetOrElse(...)` | [GALA-E0047](/docs/errors/gala-e0047/) |
| `c ? a : b` (not Go, but often reached for) | `if (c) a else b` is an expression | Parse error |
| `switch x { case 1: ... }` | `x match { case 1 => ... case _ => ... }` ([below](#switch-match-or-an-if-chain)) | [GALA-E0123](/docs/errors/gala-e0123/) |
| `case 1, 2:` | `case 1 \| 2 =>` | [GALA-E0123](/docs/errors/gala-e0123/) |
| `case MaxSize:` (a constant) | `case MaxSize =>`: a capitalized name in scope compares | [GALA-E0123](/docs/errors/gala-e0123/) |
| `switch { case x < 10: ... }` | `if (x < 10) ... else if ...`, or a match with guards | [GALA-E0123](/docs/errors/gala-e0123/) |
| `switch v := x.(type)` | `x match { case v: int => ... }` | [GALA-E0123](/docs/errors/gala-e0123/) |
| `fallthrough` | Combine the cases with `\|`, or call a shared function | [GALA-E0036](/docs/errors/gala-e0036/) |
| `goto` | Structured control flow | [GALA-E0036](/docs/errors/gala-e0036/) (with a label: [GALA-E0023](/docs/errors/gala-e0023/), undefined label) |
| `for i := 0; i < n; i++`, `for cond`, `for` | Same | Works |
| `for i, v := range xs`, `range m`, `range "str"`, `range 10` | Same, over a Go slice, map, string or integer; over a GALA `Array` or `List`, use `ForEach`, `Map`, `Filter`, `FoldLeft`, or `ZipWithIndex()` for the index | Works |
| `break`, `continue` | Same | Works |
| `break outer` / `continue outer` (labels) | None: a flag `var`, or a function and `return` | [GALA-E0129](/docs/errors/gala-e0129/) |

## Builtins and expressions

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `len(s)` | `s.Size()` (characters of a string, elements of a collection or Go slice) or `s.ByteSize()` (bytes, which is what Go's `len` counts on a string) | [GALA-E0035](/docs/errors/gala-e0035/) |
| `cap(s)` | `go_interop.SliceCap(s)` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `append(s, x)`, `append(s, t...)` | `go_interop.SliceAppend(s, x)`, `SliceAppendAll(s, t)`; `Array.Append` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `make([]T, n)`, `make([]T, 0, n)` | `go_interop.SliceWithSize[T](n)`, `SliceWithCapacity[T](n)` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `make(map[K]V)` | `go_interop.MapEmpty[K, V]()`, or `EmptyHashMap[K, V]()` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `new(T)` | `go_interop.New[T]()`, or `&T(...)` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `delete(m, k)` | `go_interop.MapDelete(m, k)`; `HashMap.Remove(k)` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `copy(dst, src)` | `go_interop.SliceCopy(src)` returns a new copy; filling an existing `dst` in place has no helper (write a loop, or keep it in Go) | [GALA-E0035](/docs/errors/gala-e0035/) |
| `close(ch)` | `go_interop.CloseChan(ch)`, `CloseSignal(s)` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `complex`, `real`, `imag` | `go_interop.Complex`, `Real`, `Imag` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `panic(v)` | `Failure(err)` in a `Try`; `go_builtins.Panic(v)` for a broken invariant ([below](#panic-and-recover-try-first)) | [GALA-E0035](/docs/errors/gala-e0035/) |
| `recover()` | `Try(() => ...)` turns a panic into a `Failure` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `min(a, b)`, `max(a, b)`, `clear(m)` | `if (a < b) a else b`; `slices.Min`/`slices.Max` on a Go slice; a new map from `MapEmpty` | [GALA-E0035](/docs/errors/gala-e0035/) |
| `println(x)`, `print(x)` | `Println(x)`, `Print(x)`: built in, no import | Works (Go's builtin, writing to stderr) |
| `[]int{1, 2}` | `go_interop.SliceOf(1, 2)`; `ArrayOf(1, 2)` | [GALA-E0007](/docs/errors/gala-e0007/) |
| `map[string]int{"a": 1}` | `HashMapOf(("a", 1))`; `go_interop.MapEmpty` and `MapPut` | [GALA-E0008](/docs/errors/gala-e0008/) |
| `[]byte(s)`, `[]rune(s)` | `go_interop.ToBytes(s)`, `ToRunes(s)` | [GALA-E0040](/docs/errors/gala-e0040/) |
| `string(b)`, `int(f)`, `float64(n)`, `string(rune(65))` | Same | Works |
| `s[a:b]`, `s[a:]`, `s[:b]` | `go_interop.Slice(s, a, b)`, `SliceDrop(s, a)`, `SliceTake(s, b)`; on a string, `strings` or a GALA string method | [GALA-E0128](/docs/errors/gala-e0128/) |
| `s[i]`, `m[k]`, `m[k] = v` | Same | Works |
| `fmt.Sprintf("%d items", n)` | Works; `s"${n} items"` and `f"${x}%.2f"` are the GALA forms | Works |
| `fmt.Println(a, b)`, `fmt.Print(a)` | `Println(a, b)`, `Print(a)`: built in, no import | Works |
| `&Point{X: 1}` | Same, or `&Point(X = 1)` | Works |
| `a == b` on structs | Same, and compares with the generated `Equal` | Works |
| `x &^ y`, `<<`, `^` | Same | Works |
| `err != nil` | Same | Works |
| `func find(id int) *User` that returns `nil` when absent, `if u == nil` | `func find(id int) Option[User]` returning `Some(u)` / `None()`, and `match` on it; a Go pointer that may be nil becomes `OptionPtrOf(p)` | Works |

## Slices and maps

The table above covers each builtin; [the worked section](#slices-and-maps-go_interop-at-the-boundary) shows them together. In short: GALA has Go slice and map **types** (`[]T`, `map[K]V`, wherever a type is expected), but no literals, slice expressions or builtins for them. Build and reshape Go values with `go_interop`, and prefer GALA's immutable `Array`, `List`, `HashMap` and `HashSet` for everything that does not cross into Go.

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `[]string` in a signature or field | Same | Works |
| `var s []int` (nil slice) | Same | Works |
| `for _, x := range xs` + `append` to filter | `xs.Filter(...)` on an `Array`, or `ArrayFromSlice(xs).Filter(...).ToGoSlice()` at the boundary | — |
| `sort.Slice(xs, less)` | `Array.SortWith(less)`, `Sorted()`, `SortBy(key)` | — |
| `strings.Join(xs, ",")` | Same, or `Array.MkString(",")` | Works |

## Errors, panic and defer

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `v, err := f(); if err != nil { return err }` | `f()` is a `Try[T]`: `Map`, `FlatMap`, `match`, or a `bind` block | Works (raw values) |
| a function returning `(T, error)` | GALA-shaped: return `Try[T]`; Go-shaped: keep `(T, error)` and give a `Try` in the body | Works |
| a function returning only `error` | Same; callers wrap the call in `Try(...)` or `FromError(...)` to get a `Try[Void]` | Works |
| `errors.New`, `fmt.Errorf("%w")`, `errors.Is`, `errors.As` | Same | Works |
| `defer f.Close()` | `use f = acquire`, or `resource.Using(f, (f) => ...)` | [GALA-E0036](/docs/errors/gala-e0036/) |
| `defer mu.Unlock()` | `resource.WithLock(mu, () => ...)` | [GALA-E0036](/docs/errors/gala-e0036/) |
| `defer` that changes named results or runs arbitrary code | None: restructure, or keep the function in a `.go` file | [GALA-E0036](/docs/errors/gala-e0036/) |

## Concurrency

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `go f()` | `go_interop.Spawn(() => f())`; or a `Future` from `concurrent` | [GALA-E0036](/docs/errors/gala-e0036/) |
| fan-out with goroutines and a result channel | `Traverse(items, (x) => Future(...))`, `Sequence`, then `Await()` ([below](#goroutines-channels-and-select)) | — |
| `chan T`, `make(chan T)`, `ch <- v` | None: a `Future`/`Promise`, `EventBus` from `concurrent`, or a `.go` file that owns the channel | [GALA-E0055](/docs/errors/gala-e0055/) for `chan T`; [GALA-E0035](/docs/errors/gala-e0035/) for `make(chan T)`; parse error for `ch <- v` |
| `<-ch` on a channel a Go API returns | Same | Works |
| `chan struct{}` as a done signal | `go_interop.NewSignal()`, `CloseSignal`, `WaitClosed`, `IsClosed` | [GALA-E0055](/docs/errors/gala-e0055/) |
| `select` with `time.After` | `go_interop.WaitClosedTimeout(ch, d)`, `Future.AwaitFor(d)`, `WithTimeout` | [GALA-E0055](/docs/errors/gala-e0055/) |
| `<-ctx.Done()` | Same, or `go_interop.WaitDone(ctx)`, `IsDone(ctx)`, `WaitDoneTimeout(ctx, d)` | Works |
| `var wg sync.WaitGroup`, `var mu sync.Mutex`, `sync.Once`, `atomic.Int64` | Same, in a `var` | Works |
| a goroutine capturing a mutable variable | A `Future` body may capture only immutable values; `Spawn` is unchecked | [GALA-E0037](/docs/errors/gala-e0037/) in a `Future` |

## Packages and files

| Go | GALA | If you write the Go form |
|----|------|--------------------------|
| `package`, `import`, `import x "pkg"`, `import _ "pkg"`, `import . "pkg"` | Same | Works |
| `go.mod` only | Add `gala.mod` with `gala mod init`; list Go dependencies with `gala mod add <module>@<version> --go` | `gala build` stops: "gala.mod not found"; a Go package no `gala.mod` module provides is [GALA-E0020](/docs/errors/gala-e0020/) |
| an unused import | Remove it, or write `import _ "pkg"` for its initialization | [GALA-E0120](/docs/errors/gala-e0120/) (as in Go, an error) |
| an unused local variable | Remove it | [GALA-E0102](/docs/errors/gala-e0102/) (as in Go, an error) |
| `//go:embed file` | `embed val content = "file"` ([Embedding Files](/docs/language-reference/#14-embedding-files)) | — |
| `//go:build linux`, `//go:generate` | Put the platform-specific or generated code in a `.go` file of the package, which keeps its directives | — |
| `x_test.go` with `testing` | Same, next to `.gala` files; GALA tests are `x_test.gala` | Works (`gala test ./...` runs both) |
| `internal/` packages | Same rule | [GALA-E0041](/docs/errors/gala-e0041/) |
| cgo, `unsafe` | A `.go` file | — |

---

## Worked sections

The listings below are from the package [`examples/porting_from_go/orders`](https://github.com/martianoff/gala/tree/master/examples/porting_from_go/orders), which mixes `.gala` files with one hand-written `.go` file. [`main.gala`](https://github.com/martianoff/gala/blob/master/examples/porting_from_go/main.gala) calls every function, and `bazel test //examples/porting_from_go/...` checks what it prints against [`main.out`](https://github.com/martianoff/gala/blob/master/examples/porting_from_go/main.out).

### `switch`: match or an if-chain

GALA has no `switch`. A switch on values becomes a `match` expression, which returns a value:

<!-- doc-source: examples/porting_from_go/orders/switch.gala -->
```gala
// StatusName names an order status code. A switch on literal values is a
// match with literal patterns: Go's `case "P", "S":` is the alternative
// pattern `"P" | "S"`, and `default:` is `case _`.
func StatusName(code string) string = code match {
    case "N" => "new"
    case "P" | "S" => "in progress"
    case "D" => "delivered"
    case _ => "unknown"
}
```

A Go `case` often names a constant. In a GALA pattern, a **capitalized** name that is a value in scope compares with it, as Go's `case` does; a lowercase name is a new binding that matches anything ([Stable Identifiers](/docs/language-reference/#stable-identifiers-constants-in-patterns)):

<!-- doc-source: examples/porting_from_go/orders/switch.gala -->
```gala
// Priority is an alias of string; its values play the part of Go constants.
type Priority string

// Urgent is the priority of an order that ships first.
val Urgent Priority = "urgent"

// Normal is the priority of every other order.
val Normal Priority = "normal"

// Weight ranks a priority. A capitalized name in a pattern that names a value
// in scope compares with it, as a Go case on a constant does; a lowercase name
// would bind instead.
func Weight(p Priority) int = p match {
    case Urgent => 10
    case Normal => 1
    case _ => 0
}
```

A switch with no tag tests unrelated conditions, which is an if-chain. When every case tests the same value, a match with guards reads well too:

<!-- doc-source: examples/porting_from_go/orders/switch.gala -->
```gala
// SizeClass classifies a quantity. A switch with no tag
// (`switch { case qty < 10: ... }`) is an if-chain...
func SizeClass(qty int) string =
    if (qty < 10) "small"
    else if (qty < 100) "medium"
    else "bulk"

// SizeClassMatch is SizeClass as a match with guards, which reads well when
// every case tests the same value.
func SizeClassMatch(qty int) string = qty match {
    case n if n < 10 => "small"
    case n if n < 100 => "medium"
    case _ => "bulk"
}
```

A type switch is a match with type patterns, which also replaces the comma-ok type assertion `s, ok := v.(string)`:

<!-- doc-source: examples/porting_from_go/orders/switch.gala -->
```gala
// Describe is a type switch (`switch v := x.(type)`): a match with type
// patterns.
func Describe(v any) string = v match {
    case s: string => s"text ${s}"
    case n: int => s"number ${n}"
    case _ => "something else"
}
```

`fallthrough` has no form. Two cases that share a body are one case with `|`. A case that does its own work and then the next case's work calls a function from both.

On a [sealed type](/docs/language-reference/#sealed-types-algebraic-data-types), a match must cover every case, and adding a case turns every incomplete match into a compile error ([GALA-E0002](/docs/errors/gala-e0002/)). That is the main reason to port a Go `iota` enumeration to a sealed type rather than to a set of values.

### `(T, error)`: Try, or Go's result list

A Go call that returns `(T, error)` is a `Try[T]` in GALA, `Success(v)` or `Failure(err)`. A call that returns several values without an error is a Tuple. So a function that parses one line is one expression, with no `if err != nil`:

<!-- doc-source: examples/porting_from_go/orders/errors.gala -->
```gala
// Line is one "SKU,QTY" line of an order.
struct Line(SKU string, Qty int)

// ParseLine is GALA-shaped: one Try value in place of (Line, error).
// strings.Cut returns three results, so it is a Tuple3; strconv.Atoi returns
// (int, error), so it is a Try[int].
func ParseLine(text string) Try[Line] = strings.Cut(text, ",") match {
    case (sku, qty, true) => strconv.Atoi(strings.TrimSpace(qty)).Map((n) => Line(sku, n))
    case _ => Failure(fmt.Errorf("line %q: want SKU,QTY", text))
}
```

Errors chain through `Map` and `FlatMap`: the first failure stops the rest, as an early `return err` does in Go.

<!-- doc-source: examples/porting_from_go/orders/errors.gala -->
<!-- doc-check: fragment -->
```gala
// Total adds the quantities of several lines; the first bad line fails it.
func Total(lines ...string) Try[int] = ArrayOf(lines...).FoldLeft(Success(0), (acc, line) =>
    acc.FlatMap((sum) => ParseLine(line).Map((l) => sum + l.Qty)))
```

A function that Go code calls can keep Go's result list. The body still gives a `Try`, and Go receives `(value, nil)` or `(zero, err)`:

<!-- doc-source: examples/porting_from_go/orders/errors.gala -->
```gala
// ParseQty is Go-shaped: it declares Go's result list, so a Go caller gets
// (int, error). The body gives the Try that the list stands for.
func ParseQty(text string) (int, error) = strconv.Atoi(strings.TrimSpace(text))
```

`return n, nil` and `return 0, err` are not GALA: a GALA function returns one value, so write `Success(n)` and `Failure(err)`. Named results have no form either. The `if` initializer is rejected with a hint that names the `Try` forms:

<!-- doc-check: error GALA-E0047 -->
```gala
import "strconv"

func parse(text string) int {
    if n, err := strconv.Atoi(text); err == nil {
        return n
    }
    return 0
}
```

[Go functions that return several results](/docs/language-reference/#go-functions-that-return-several-results) and [Returning several results to Go](/docs/language-reference/#returning-several-results-to-go) have the full rules.

### `panic` and `recover`: Try first

A failure the caller can do something about is a `Failure`, not a panic. For a broken invariant, which only a bug can cause, `go_builtins.Panic` is Go's `panic`. Go's `recover` is `Try`, which runs its argument and turns a panic in it into a `Failure` that holds the panic's error:

<!-- doc-source: examples/porting_from_go/orders/errors.gala -->
```gala
// checkedQty guards an invariant that only a bug can break: that is what
// go_builtins.Panic is for. A failure the caller can act on is a Try.
func checkedQty(n int) int {
    if (n < 0) {
        go_builtins.Panic(s"negative quantity ${n}")
    }
    n
}

// Recovered is Go's recover(): Try runs its argument, which it takes by name,
// and turns a panic in it into a Failure that holds the panic's error.
func Recovered(n int) Try[int] = Try(checkedQty(n))
```

`Recovered(-1)` is `Failure(negative quantity -1)`. `Try`'s parameter is by-name, so `Try(checkedQty(n))` is `Try(() => checkedQty(n))`: the call runs inside `Try`. `.Get()` on a `Failure` panics with its error, so `ParseLine(s).Get()` is Go's `must` idiom. Unlike `recover`, `Try` does not resume a function in the middle: the work that may panic goes inside the `Try`.

### `defer`: `use` and the resource package

GALA has no `defer` ([GALA-E0036](/docs/errors/gala-e0036/)). The usual `defer` releases something, and that has two forms. `use x = acquire` binds `x` and closes it when the function returns, on every path; several `use` bindings close in reverse order. `os.Open` returns `(*os.File, error)`, which is a `Try`, so the code that reads the file goes in a `Map`, which runs only when the open worked:

<!-- doc-source: examples/porting_from_go/orders/cleanup.gala -->
```gala
func CountLines(path string) Try[int] = os.Open(path).Map((f) => {
    use file = f
    val scanner = bufio.NewScanner(file)
    var n = 0
    for scanner.Scan() {
        n++
    }
    n
})
```

`Using` from the `resource` package does the same for a resource you already hold, and `WithLock` holds a mutex for its body, unlocking it when the body returns or panics:

<!-- doc-source: examples/porting_from_go/orders/cleanup.gala -->
```gala
func FirstLine(path string) Try[string] = os.Open(path).Map((f) => Using(f, (file) => {
    val scanner = bufio.NewScanner(file)
    if (scanner.Scan()) scanner.Text() else ""
}))
```

<!-- doc-source: examples/porting_from_go/orders/cleanup.gala -->
```gala
struct Tally(mu *go_interop.Mutex, var count int)

// NewTally returns a Tally at zero.
func NewTally() *Tally = &Tally(go_interop.NewMutex(), 0)

// Add adds n under the lock and returns the new count.
func (t *Tally) Add(n int) int = WithLock(t.mu, () => {
    t.count = t.count + n
    t.count
})
```

To read a file line by line, `fs.ReadLines` and `fs.ForEachLine` open, scan and close it for you; the scanner above is there to show `use`. `Bracket(resource, release, body)` takes any release function, for a resource without `Close() error`. A `defer` that does something other than release a resource, such as changing a named result or logging on the way out, has no GALA form; restructure the function, or keep it in a `.go` file.

### Slices and maps: go_interop at the boundary

Go slice and map types work in signatures, but their literals, slice expressions and builtins have no GALA syntax. A function that Go calls with slices and maps uses the `go_interop` helpers:

<!-- doc-source: examples/porting_from_go/orders/slices.gala -->
```gala
func SKUs(lines []string) []string {
    var out = go_interop.SliceWithCapacity[string](lines.Size())  // make([]string, 0, len(lines))
    for _, line := range go_interop.SliceDrop(lines, 1) {         // lines[1:]
        val (sku, _, found) = strings.Cut(line, ",")
        if (found) {
            out = go_interop.SliceAppend(out, sku)                 // append(out, sku)
        }
    }
    out
}

// Stock builds a Go map, for a Go caller.
func Stock(skus []string) map[string]int {
    val m = go_interop.MapEmpty[string, int]()                      // map[string]int{}
    for i, sku := range skus {
        go_interop.MapPut(m, sku, i + 1)                            // m[sku] = i + 1
    }
    go_interop.MapDelete(m, "B-2")                                  // delete(m, "B-2")
    m
}

// Lookup is the comma-ok read `v, ok := m[k]`, as an Option.
func Lookup(m map[string]int, sku string) Option[int] = go_interop.OptionFromMap(m, sku)
```

Inside GALA code, convert once and use the immutable collections, whose methods replace most loops:

<!-- doc-source: examples/porting_from_go/orders/slices.gala -->
```gala
func Reorder(stock map[string]int, threshold int) Array[string] =
    HashMapFromGoMap(stock)
        .Filter((_, qty) => qty < threshold)
        .Keys()
        .Sorted()
```

`ArrayFromSlice`, `HashMapFromGoMap` and `HashSetFromSlice` copy a Go value in; `.ToGoSlice()` and `.ToGoMap()` copy it out. The full list of helpers is in [Go built-in functions](/docs/language-reference/#11-go-built-in-functions) and [Go slices and maps](/docs/go-interop/#go-slices-and-maps). GALA has no slice-expression syntax by design: slices are a Go-interop type, not a GALA one.

### Struct tags: a codec, or a Go file

GALA has no struct tags ([GALA-E0085](/docs/errors/gala-e0085/)). Which way out depends on who reads the tags.

If your own code encodes the type, use GALA's `json` or `yaml` codec and set the keys on it. The codec reads the struct at compile time, without reflection:

<!-- doc-source: examples/porting_from_go/orders/codec.gala -->
```gala
// Order is a GALA struct. Its JSON keys are set on the codec, not in tags:
// snake_case by default, "id" for ID, and Note left out when it is empty.
struct Order(ID string, LineCount int, Note string)

val orderCodec = json.Codec[Order](json.SnakeCase()).Rename("ID", "id").OmitEmpty("Note")

// EncodeOrder writes an order as JSON.
func EncodeOrder(o Order) Try[string] = orderCodec.Encode(o)
```

`EncodeOrder(Order("o-1", 2, ""))` is `Success({"id":"o-1","line_count":2})`.

If a Go library reads the tags (`encoding/json` on a type you hand it, an ORM, a validator), declare the type in a `.go` file of the same package. The `.gala` files use it like any Go type:

<!-- doc-source: examples/porting_from_go/orders/wire.go -->
```go
// WireOrder keeps its struct tags, so it stays in Go: GALA has no struct
// tags (GALA-E0085). The .gala files of the package use it like any Go type.
type WireOrder struct {
	ID    string `json:"id"`
	Lines int    `json:"line_count,omitempty"`
}
```

<!-- doc-source: examples/porting_from_go/orders/codec.gala -->
<!-- doc-check: fragment -->
```gala
func EncodeWire(id string, lines int) Try[string] =
    gojson.Marshal(WireOrder(ID = id, Lines = lines)).Map((b) => string(b))
```

The Go package `encoding/json` is imported as `gojson` because GALA's own `json` package has the same name.

### `const`, exported values, and methods on scalar types

GALA has no `const`. A package-level `val` cannot be reassigned, which is what a Go constant is usually for:

<!-- doc-source: examples/porting_from_go/orders/config.gala -->
```gala
// MaxLines was `const MaxLines = 100`. GALA has no const: a package-level
// val cannot be reassigned. Go code reads it as MaxLines.Get() (see wire.go).
val MaxLines = 100

// Verbose was `var Verbose = false`. A package-level var stays a plain Go
// variable that Go code reads and sets directly.
var Verbose = false
```

GALA code reads `MaxLines` as an `int`. Go code sees a `std.Immutable[int]`, read with `.Get()`, while the `var` is a plain Go variable:

<!-- doc-source: examples/porting_from_go/orders/wire.go -->
```go
// LineLimit reads the GALA package-level val MaxLines. Go sees a val as a
// std.Immutable and reads it with Get(); a var would be a plain variable.
func LineLimit() int {
	return MaxLines.Get()
}

// EnableVerbose sets the GALA package-level var Verbose, a plain Go variable.
func EnableVerbose() {
	Verbose = true
}
```

So a value that Go code reads or sets is a `var`, or a function. A name that must be a Go compile-time constant, such as an array length or a constant in a Go API, stays in a `.go` file, and `.gala` files of the package can still use it, also as a [stable identifier](#switch-match-or-an-if-chain) in a `case`.

In Go, `type Cents int64` is a new type that can have methods. In GALA, `type X Y` is an alias, and a method on an alias of a type from another package (`int64` included) is [GALA-E0048](/docs/errors/gala-e0048/). The distinct type with methods is an `opaque type`:

<!-- doc-source: examples/porting_from_go/orders/config.gala -->
```gala
// Cents was `type Cents int64` with methods. `type Cents int64` is an alias in
// GALA, so a distinct type with its own methods is an opaque type.
opaque type Cents int64

// String formats an amount in dollars, such as $7.50 or $-2.50.
func (c Cents) String() string = f"$$${float64(int64(c)) / 100}%.2f"

// Price converts explicitly, as Go does.
func Price(units int, each Cents) Cents = Cents(int64(units) * int64(each))
```

Conversions are explicit both ways, as in Go, and Go code sees an ordinary `type Cents int64` with its methods. [Opaque Types](/docs/language-reference/#opaque-types) has the rules.

### Goroutines, channels and `select`

GALA has no `go` statement, channel types or `select` ([GALA-E0036](/docs/errors/gala-e0036/), [GALA-E0055](/docs/errors/gala-e0055/)). Most goroutine-and-channel code computes values in parallel and collects them; that is a `Future`:

<!-- doc-source: examples/porting_from_go/orders/workers.gala -->
```gala
// WordCounts is the fan-out/fan-in of Go's
//
//	results := make(chan int, len(texts))
//	for _, t := range texts { go func() { results <- count(t) }() }
//	counts := make([]int, 0, len(texts))
//	for range texts { counts = append(counts, <-results) }
//
// Each text is counted in a Future; Traverse collects the results in the
// order of the texts (the Go loop gets them in completion order),
// and Await waits for them. A panic in a body fails the Try instead of
// crashing the program.
func WordCounts(texts Array[string]) Try[Array[int]] =
    Traverse(texts, (t) => Future(strings.Fields(t).Size())).Await()
```

A `Future` body may capture only immutable values, which the compiler checks ([GALA-E0037](/docs/errors/gala-e0037/)); a goroutine that writes a shared variable is a data race GALA does not let you write there. `Future` also has `Zip`, `Sequence`, `Race`, `WithTimeout` and `AwaitFor(d)`; see [Concurrent](https://github.com/martianoff/gala/blob/master/docs/CONCURRENT.MD).

A channel that only signals "done" is a `go_interop.Signal`, and a `select` that waits for it or for a timeout is `WaitClosedTimeout`:

<!-- doc-source: examples/porting_from_go/orders/workers.gala -->
```gala
func FinishesWithin(work func(), d time.Duration) bool {
    val done = go_interop.NewSignal()
    go_interop.Spawn(() => {
        work()
        go_interop.CloseSignal(done)
    })
    go_interop.WaitClosedTimeout(done, d)
}
```

When the work produces a value, prefer a `Future` and its `AwaitFor(d)` or `WithTimeout(d)`, which carry the result and check captures. `Spawn` is Go's `go` statement with no capture check. Receiving from a channel that a Go API returns works as in Go (`<-ctx.Done()`). Code that creates channels of values, sends on them or selects over several of them (a worker pool, a pipeline, a server loop) has no GALA form: keep it in a `.go` file of the package and call it from GALA.

---

## What stays in Go

GALA does not try to express everything Go can. Keep these in a `.go` file of the same package, where the `.gala` files can call them:

- types whose struct tags a Go library reads, and types with embedded fields;
- channels of values, `select` over several channels, and `defer` that is not a release;
- `const` blocks and `iota` values that Go code relies on as constants, and fixed-size arrays such as `[16]byte`;
- type-set constraints such as `~int | ~float64`;
- labeled `break`/`continue`, `goto` and `fallthrough`, when restructuring is not worth it;
- cgo, `unsafe`, `//go:build` and `//go:generate` directives, and generated code.

A mixed package builds with `gala build`, `gala test` and Bazel ([One package, two languages](/docs/go-interop/#part-3-one-package-two-languages)), so moving a function between the two languages does not change its callers.

{% endraw %}

## Related

- [Add GALA to a Go Repository](/docs/add-gala-to-go-repo/) — `gala mod init` in an existing Go module, and a cobra CLI written in GALA
- [Go Interop Guide](/docs/go-interop/) — the full rules for mixing GALA and Go in both directions
- [Language Reference](/docs/language-reference/) — the syntax and semantics behind every row on this page
- [Error Codes](/docs/errors/) — every `GALA-Exxxx` diagnostic named on this page
