# Go Interop Guide: Mixing GALA and Go

GALA transpiles to Go, so a GALA package *is* a Go package once it is built. GALA code calls any Go package directly, and Go code calls GALA packages the way it calls other Go packages. The two languages can even share one package. This guide covers both directions with worked examples, how to lay out and build a project that uses both, and the gotchas to know before you start.

For a shorter overview of calling Go from GALA, see [Go Interop](https://gala.fyi/features/go-interop/) on the website. The full rules are in the [language reference](GALA.MD) ([Go functions that return several results](GALA.MD#go-functions-that-return-several-results), [Go built-in functions](GALA.MD#11-go-built-in-functions-are-forbidden), [where Go slice and map types may be written](GALA.MD#where-go-slice-and-map-types-may-be-written)).

## Table of Contents

1. [The examples](#the-examples)
2. [Part 1: Calling Go from GALA](#part-1-calling-go-from-gala)
3. [Part 2: Calling GALA from Go](#part-2-calling-gala-from-go)
4. [Part 3: One package, two languages](#part-3-one-package-two-languages)
5. [Part 4: Building a mixed project](#part-4-building-a-mixed-project)
6. [Gotchas and limitations](#gotchas-and-limitations)

---

## The examples

Every program and Go snippet in this guide is taken from a file in the repository, every one of those files is a test, and a doc test (`//internal/doccheck`) fails when a listing here stops matching its file. The Bazel examples run in `bazel test //examples/go_interop/...`. The `gala build` project runs in the CLI-path CI job, which builds the real CLI with an empty `GALA_HOME`.

| Section | Files | Test |
|---------|-------|------|
| Calling Go | [`examples/go_interop/gala_calls_go/main.gala`](../examples/go_interop/gala_calls_go/main.gala), Go library [`warehouse/warehouse.go`](../examples/go_interop/warehouse/warehouse.go) | `//examples/go_interop:gala_calls_go` |
| Implementing Go interfaces | [`examples/go_interop/go_interfaces/main.gala`](../examples/go_interop/go_interfaces/main.gala) | `//examples/go_interop:go_interfaces` |
| Calling GALA from Go | GALA library [`pricing/pricing.gala`](../examples/go_interop/pricing/pricing.gala), Go code [`checkout/checkout.go`](../examples/go_interop/checkout/checkout.go) with Go `Example` tests | `//examples/go_interop:checkout_test` |
| One package, two languages | [`mixed/textstats/textstats.gala`](../examples/go_interop/mixed/textstats/textstats.gala) + [`writer.go`](../examples/go_interop/mixed/textstats/writer.go) | `//examples/go_interop:mixed` |
| A `gala build` project | [`tools/ci/cli_path/testdata/go_interop/`](../tools/ci/cli_path/testdata/go_interop) | `go_interop` fixture of `tools/ci/cli_path/run_fixtures.sh` |

---

## Part 1: Calling Go from GALA

### Importing Go packages

A Go import looks exactly like a Go import. Single, grouped, aliased and dot imports all work, for the standard library, for Go packages in your own module, and for third-party modules. [`gala_calls_go/main.gala`](../examples/go_interop/gala_calls_go/main.gala) imports the standard library, a GALA package, a hand-written Go package of the same module, and GALA's Go helpers:

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
```gala
import (
    "errors"
    "strconv"
    "strings"
    "time"

    . "martianoff/gala/collection_immutable"
    "martianoff/gala/examples/go_interop/warehouse"
    "martianoff/gala/go_interop"
)
```

The transpiler decides per import whether the package is GALA or Go. A package is GALA if it is in the current module and contains `.gala` files, or if it comes from a `gala.mod` requirement that is not marked `// go`. Everything else is Go, and its types are read from the Go source by the Go SDK. Nothing has to be declared or generated for a Go package.

A **third-party Go module** is added with `gala mod add github.com/google/uuid@v1.6.0 --go`, which writes `github.com/google/uuid v1.6.0 // go` to `gala.mod`. Under Bazel it comes from Gazelle's `go_deps` like any Go dependency. See [Go Dependencies](DEPENDENCY_MANAGEMENT.MD#6-go-dependencies).

When a Go package and a GALA package have the same name (`strings`, `io`, `json`, `fs`, `regex`, …), the GALA one keeps the name and you alias the Go one: `gostrings "strings"`.

### Functions, methods, structs and pointers

Go calls look like Go calls. A Go struct is built with named arguments, a Go constructor that returns a pointer works as is, and methods are called through the pointer:

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
<!-- doc-check: fragment -->
```gala
// Go standard library calls look like Go calls.
Println(strings.ToUpper("gala"), strings.Repeat("-", 3), time.Duration(90) * time.Second)

// A Go constructor returning a pointer; methods work through it.
val inv = warehouse.New()
inv.Add(warehouse.Item(SKU = "A-1", Name = "Anvil", Qty = 3, Price = 99.5))
```

Go field reads (`item.Qty`), Go constants (`time.Second`, `http.StatusNoContent`), variadic calls with spread (`ArrayOf(strings.Split(line, ",")...)`) and `nil` arguments all work as in Go.

### Go functions that return several results

GALA turns a Go call that returns several results into **one value**:

| Go returns | A GALA value of type | Read it with |
|------------|----------------------|--------------|
| `(T, error)` | `Try[T]` | `match`, `GetOrElse`, `Map`, `FlatMap`, `bind` |
| `(A, B)`, including `(T, bool)` | `Tuple[A, B]` | `val (a, b) = …` |
| `(A, B, C)` … up to 10 values | `Tuple3` … `Tuple10` | `val (a, b, c) = …` |
| `(A, B, error)` | `Try[Tuple[A, B]]` | `case Success((a, b)) =>` |
| `error` only | `error` | `FromError(call)` or `Try(call)` gives `Try[Void]` |

This holds for functions and methods alike, from any Go package:

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
<!-- doc-check: fragment -->
```gala
// (T, bool) is a Tuple; destructure it.
func describe(inv *warehouse.Inventory, sku string) string {
    val (item, found) = inv.Find(sku)
    if (found) s"${item.Name} x${item.Qty}" else s"$sku: no such item"
}

// (T, error) is a Try; match on it. A guard can test the Go error.
func reserve(inv *warehouse.Inventory, sku string, qty int) string = inv.Reserve(sku, qty) match {
    case Success(left) => s"reserved $qty of $sku, $left left"
    case Failure(err) if errors.Is(err, warehouse.ErrOutOfStock) => s"$sku: not enough stock for $qty"
    case Failure(err) => s"failed: ${err.Error()}"
}

// Several fallible Go calls, sequenced with bind.
func parseItem(line string) Try[warehouse.Item] = ArrayOf(strings.Split(line, ",")...) match {
    case Array(sku, name, qty, price) => {
        bind q = strconv.Atoi(qty)
        bind p = strconv.ParseFloat(price, 64)
        Success(warehouse.Item(SKU = sku, Name = name, Qty = q, Price = p))
    }
    case _ => Failure(errors.New(s"want 4 fields: $line"))
}
```

An error-only call becomes a `Try[Void]` with `FromError`:

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
<!-- doc-check: fragment -->
```gala
// An error-only Go call becomes a Try[Void] with FromError.
Println(FromError(inv.Remove("Z-9")).IsFailure())
```

To hand raw results straight back to Go, bind several names: `val n, err = strconv.Atoi(s)` gives Go's two values, with no `Try`. Prefer the `Try` in GALA code. Using a `Try` or Tuple where the plain `T` is expected is [GALA-E0049](errors/GALA-E0049.md).

### Passing lambdas to Go

A lambda passed where Go expects a function takes its parameter types from the Go signature, including Go **named function types** such as `warehouse.Predicate` (`func(Item) bool`), `http.HandlerFunc` or `fs.WalkDirFunc`:

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
<!-- doc-check: fragment -->
```gala
// A lambda fills a Go named function type (warehouse.Predicate).
val cheap = ArrayFromSlice(inv.Select((i) => i.Price < 5.0))
```

A Go function type with **several results**, such as `func() (int, error)`, takes a lambda too. The lambda gives their one GALA value, a `Try` for `(T, error)` or a Tuple for `(A, B)`, and Go receives the results. A body that is a Go call with those results returns them as they are, and a generic Go function takes its type arguments from the lambda's results ([Returning several results to Go](GALA.MD#returning-several-results-to-go)):

```gala
import (
    "strconv"
    "sync"
)

val port = sync.OnceValues(() => strconv.Atoi("8080")) // a func() (int, error)
val p, err = port()
```

### Implementing Go interfaces

A GALA type satisfies a Go interface the way a Go type does: by having the methods. No declaration is needed. From [`go_interfaces/main.gala`](../examples/go_interop/go_interfaces/main.gala):

<!-- doc-source: examples/go_interop/go_interfaces/main.gala -->
```gala
// error: an Error() string method.
struct NotFound(Path string)

func (e NotFound) Error() string = s"not found: ${e.Path}"

// fmt.Stringer: a String() string method; fmt and string interpolation use
// it. Money is an opaque type: a distinct int64 with methods of its own.
opaque type Money int64

func (m Money) String() string = f"$$${float64(m) / 100.0}%.2f"

// sort.Interface over a Go slice held in a field.
struct ByLength(Words []string)

func (b ByLength) Len() int = b.Words.Size()

func (b ByLength) Less(i int, j int) bool = b.Words[i].Size() < b.Words[j].Size()

func (b ByLength) Swap(i int, j int) {
    val w = b.Words[i]
    b.Words[i] = b.Words[j]
    b.Words[j] = w
}

// http.Handler: ServeHTTP(http.ResponseWriter, *http.Request).
struct Greeter(Greeting string)

func (g Greeter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    val name = r.URL.Query().Get("name")
    if (name == "") {
        http.Error(w, "missing name", http.StatusBadRequest)
    } else {
        fmt.Fprintf(w, "%s, %s!", g.Greeting, name)
    }
}
```

These values then go straight into Go APIs. A GALA error goes through `errors.As`, `sort.Sort` sorts a `ByLength`, and a `Greeter` (or a lambda converted to `http.HandlerFunc`) is served through `httptest`:

<!-- doc-source: examples/go_interop/go_interfaces/main.gala -->
<!-- doc-check: fragment -->
```gala
var target NotFound
Println(errors.As(err, &target), target.Path)

// sort.Sort drives a GALA sort.Interface.
val words = ByLength(ArrayOf("banana", "fig", "apple", "kiwi").ToGoSlice())
sort.Sort(words)

val health = http.HandlerFunc((w, r) => w.WriteHeader(http.StatusNoContent))
```

**Pointer receivers.** When the methods mutate, declare them on `*T` and pass the address of a **`var`**. `&x` of a `val` is a read-only `ConstPtr[T]`, not a `*T`, so it does not satisfy the interface. Here a GALA collector receives the Go library's `warehouse.Notifier` calls:

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
<!-- doc-check: fragment -->
```gala
struct Collector(Prefix string, var Messages Array[string])

func (c *Collector) Notify(message string) {
    c.Messages = c.Messages.Append(s"${c.Prefix}$message")
}

// A pointer to a GALA struct is passed where Go expects an interface.
var collector = Collector("ALERT ", EmptyArray[string]())
val reported = inv.CheckLowStock(10, &collector)
```

**Methods with several results** (`io.Writer`'s `Write([]byte) (int, error)`, `io.Reader`, `json.Marshaler`, `driver.Valuer`) declare Go's result list. The body gives the results' one GALA value, a `Try` for `(T, error)` or a Tuple for `(A, B)` ([Returning several results to Go](GALA.MD#returning-several-results-to-go)):

```gala
import . "martianoff/gala/go_interop"

struct LineCounter(var Lines int)

// io.Writer
func (c *LineCounter) Write(p []byte) (int, error) {
    c.Lines = c.Lines + 1
    Success(p.Size())
}
```

### Go slices and maps

Prefer GALA's collections in GALA code, and convert at the boundary:

| From | To | Use |
|------|----|-----|
| Go `[]T` | `Array[T]` / `List[T]` / `HashSet[T]` | `ArrayFromSlice(s)`, `ListFromSlice(s)`, `HashSetFromSlice(s)` (copies), or `ArrayOf(s...)` |
| `Array` / `List` / `HashSet` / `TreeSet` | Go `[]T` | `.ToGoSlice()` |
| Go `map[K]V` | `HashMap[K, V]` / `TreeMap[K, V]` | `HashMapFromGoMap(m)`, `TreeMapFromGoMap(m)` |
| `HashMap` / `TreeMap` | Go `map[K]V` | `.ToGoMap()` |

<!-- doc-source: examples/go_interop/gala_calls_go/main.gala -->
<!-- doc-check: fragment -->
```gala
// A Go slice result converts to an immutable Array.
val items = ArrayFromSlice(inv.Items())
Println(items.Map(_.SKU).MkString(", "))

// Back to Go: an Array becomes a Go slice for Go functions that take one.
val names = items.Map(_.Name).ToGoSlice()
Println(strings.Join(names, " | "))
Println(go_interop.SliceAppend(names, "Extra").Size())
```

Go slice and map **types** may be written wherever a type is expected (parameters, results, fields, annotations, aliases), but GALA has no slice or map literals ([GALA-E0007](errors/GALA-E0007.md), [GALA-E0008](errors/GALA-E0008.md)) and no slice expressions. To build or reshape a Go value, use the `martianoff/gala/go_interop` package. The common cases (the full list is in [Go built-in functions](GALA.MD#11-go-built-in-functions-are-forbidden)):

| Go | GALA |
|----|------|
| `[]int{1, 2}` | `go_interop.SliceOf(1, 2)` |
| `make([]T, n)` / `make([]T, 0, n)` | `SliceWithSize[T](n)` / `SliceWithCapacity[T](n)` |
| `append(s, x)` / `append(s, t...)` | `SliceAppend(s, x)` / `SliceAppendAll(s, t)` |
| `s[a:b]`, `s[:n]`, `s[n:]` | `Slice(s, a, b)`, `SliceTake(s, n)`, `SliceDrop(s, n)` |
| `map[K]V{}` / `make(map[K]V)` | `MapEmpty[K, V]()` |
| `m[k] = v` / `delete(m, k)` | `MapPut(m, k, v)` / `MapDelete(m, k)` |
| `v, ok := m[k]` | `OptionFromMap(m, k)` gives `Option[V]` |
| `len(x)` | `x.Size()`, or `.ByteSize()` for a string's bytes |
| `[]byte(s)` / `string(b)` / `[]rune(s)` | `ToBytes(s)` / `ToString(b)` (or `string(b)`) / `ToRunes(s)` |
| `new(T)` | `New[T]()` |

### Go features that have no GALA syntax

| Go | GALA |
|----|------|
| `go f()` | `go_interop.Spawn(() => f())`, or a `Future` from `concurrent` |
| `defer x.Close()` | `use x = open(...)`, or `Using(res, (r) => ...)` from `resource` |
| `chan T`, `select`, `close(ch)` | `Future`/`Promise`/`Stream`; `go_interop.Signal`, `CloseChan` for Go APIs that hand you one |
| `panic` / `recover` | `go_builtins.Panic` / `Try(...)` |
| `if v, err := f(); err != nil` | `f() match { ... }` or `f().GetOrElse(...)` ([GALA-E0047](errors/GALA-E0047.md)) |

The bare builtins and statement keywords are compile errors ([GALA-E0035](errors/GALA-E0035.md), [GALA-E0036](errors/GALA-E0036.md)) whose messages name the replacement.

---

## Part 2: Calling GALA from Go

A GALA package is a Go package after transpilation. Go code imports it by its import path, and nothing is generated for the Go side. What Go sees is the generated code, so it helps to know its shape.

### What a GALA declaration looks like from Go

| GALA | Go sees |
|------|---------|
| `func Subtotal(items Array[LineItem]) int64` | the same function. Default parameter values exist only for GALA callers, so Go passes every argument |
| `struct LineItem(SKU string, ...)` (fields not `var`) | `type LineItem struct { SKU std.Immutable[string]; ... }`. Read with `item.SKU.Get()`. There is no generated constructor |
| `var` field, e.g. `type Receipt struct { var TotalCents int64 }` | a plain Go field `TotalCents int64` |
| `sealed type Discount { case Percent(Pct int64) ... }` | one struct `Discount` (unexported tag), plus an empty struct per case: `Percent{}.Apply(10)` builds one, `Percent{}.Unapply(d)` returns `std.Option[int64]` |
| `Option[T]`, `Try[T]`, `Either[L, R]` | `std.Option[T]` etc.: `IsDefined()`, `IsSuccess()`, `Get()`, `GetError()`, `GetOrElse(x)` |
| a generic method, e.g. `Option.Map[U]` | a free function `std.Option_Map(o, f)` (Go methods cannot have type parameters) |
| `Array[T]`, `List[T]`, `HashMap[K, V]` | `collection_immutable.Array[T]` etc.: `ArrayOf`, `ArrayFromSlice`, `.Size()`, `.ToGoSlice()` |
| package-level `var X` / `val X` | a Go variable / a `std.Immutable` variable (read with `X.Get()`) |
| PascalCase / camelCase names | exported / unexported, exactly as in Go. GALA never renames an identifier |

Every type also gets `Copy()`, `Equal(other)` and, for sealed types, `String()`.

### A GALA library with a Go-facing API

[`pricing/pricing.gala`](../examples/go_interop/pricing/pricing.gala) is a GALA library written to be called from Go. It provides a constructor function for its immutable struct, uses `var` fields on the struct Go reads most, and returns `Option` and `Try`:

<!-- doc-source: examples/go_interop/pricing/pricing.gala -->
```gala
package pricing

import (
    "errors"
    "strconv"
    "strings"

    . "martianoff/gala/collection_immutable"
)

// LineItem is GALA-native: its fields are immutable, so Go sees each one as
// a std.Immutable[T] and builds a LineItem through NewLineItem.
struct LineItem(SKU string, UnitCents int64, Qty int)

// Receipt is Go-shaped: `var` fields are plain Go fields.
type Receipt struct {
    var TotalCents int64
    var Lines int
    var Note string
}

// Discount is a sealed type: one Go struct plus a constructor type per case.
sealed type Discount {
    case NoDiscount()
    case Percent(Pct int64)
    case AmountOff(Cents int64)
}

// ErrEmptyCart is a plain Go error value that Go can test with errors.Is.
var ErrEmptyCart = errors.New("empty cart")

// NewLineItem is the constructor Go callers use.
func NewLineItem(sku string, unitCents int64, qty int) LineItem = LineItem(sku, unitCents, qty)

// Subtotal sums an immutable Array of items.
func Subtotal(items Array[LineItem]) int64 =
    items.FoldLeft(int64(0), (acc, i) => acc + i.UnitCents * int64(i.Qty))

// Apply is a method on the sealed type.
func (d Discount) Apply(cents int64) int64 = d match {
    case NoDiscount() => cents
    case Percent(p) => cents - cents * p / 100
    case AmountOff(c) => if (c > cents) int64(0) else cents - c
}

// amount reads the number after a coupon's prefix.
func amount(code string, prefix string) Option[int64] =
    strconv.ParseInt(strings.TrimPrefix(code, prefix), 10, 64).ToOption()

// ParseCoupon returns None for a code it does not know.
func ParseCoupon(code string) Option[Discount] = code match {
    case "" => Some(NoDiscount())
    case c if strings.HasPrefix(c, "PCT") => amount(c, "PCT").Map((n) => Percent(n))
    case c if strings.HasPrefix(c, "OFF") => amount(c, "OFF").Map((n) => AmountOff(n))
    case _ => None()
}

// Checkout fails with ErrEmptyCart or an unknown-coupon error.
func Checkout(items Array[LineItem], coupon string) Try[Receipt] = ParseCoupon(coupon) match {
    case _ if items.IsEmpty() => Failure(ErrEmptyCart)
    case Some(d) => Success(Receipt(
        TotalCents = d.Apply(Subtotal(items)),
        Lines = items.Size(),
        Note = d.String(),
    ))
    case None() => Failure(errors.New(s"unknown coupon $coupon"))
}
```

### The Go side

[`checkout/checkout.go`](../examples/go_interop/checkout/checkout.go) is ordinary Go that imports `pricing`. It converts Go input into GALA values, turns a `Try` into Go's `(value, error)`, reads a sealed value and maps an `Option`:

<!-- doc-source: examples/go_interop/checkout/checkout.go -->
```go
package checkout

import (
	"errors"
	"fmt"

	"martianoff/gala/collection_immutable"
	"martianoff/gala/examples/go_interop/pricing"
	"martianoff/gala/std"
)

// Line is the Go-side input: a plain Go struct.
type Line struct {
	SKU       string
	UnitCents int64
	Qty       int
}

// toItems converts Go values into the immutable Array the GALA API takes.
func toItems(lines []Line) collection_immutable.Array[pricing.LineItem] {
	items := make([]pricing.LineItem, 0, len(lines))
	for _, l := range lines {
		items = append(items, pricing.NewLineItem(l.SKU, l.UnitCents, l.Qty))
	}
	return collection_immutable.ArrayFromSlice(items)
}

// Total runs the GALA checkout and turns its Try into Go's (value, error).
func Total(lines []Line, coupon string) (int64, error) {
	result := pricing.Checkout(toItems(lines), coupon)
	if result.IsFailure() {
		return 0, result.GetError()
	}
	return result.Get().TotalCents, nil
}

// Describe reads a GALA sealed value from Go: each case's Unapply reports
// whether the value is that case, and returns its fields.
func Describe(d pricing.Discount) string {
	if pct := (pricing.Percent{}).Unapply(d); pct.IsDefined() {
		return fmt.Sprintf("%d%% off", pct.Get())
	}
	if off := (pricing.AmountOff{}).Unapply(d); off.IsDefined() {
		return fmt.Sprintf("%d cents off", off.Get())
	}
	return "no discount"
}

// CouponLabel calls a GALA function that returns an Option and maps it with
// a Go func. Generic methods such as Option.Map are free functions in Go.
func CouponLabel(code string) string {
	label := std.Option_Map(pricing.ParseCoupon(code), Describe)
	return label.GetOrElse("unknown coupon")
}

// IsEmptyCart tests a GALA-declared Go error value with errors.Is.
func IsEmptyCart(err error) bool {
	return errors.Is(err, pricing.ErrEmptyCart)
}
```

The Go `Example` tests in [`checkout_test.go`](../examples/go_interop/checkout/checkout_test.go) check the output. One of them builds GALA values directly from Go:

<!-- doc-source: examples/go_interop/checkout/checkout_test.go -->
```go
func Example_fromGo() {
	discount := pricing.Percent{}.Apply(50)
	fmt.Println(discount, discount.Apply(900))

	item := pricing.NewLineItem("C-3", 475, 2)
	fmt.Println(item.SKU.Get(), item.Qty.Get())

	items := collection_immutable.ArrayOf(item, pricing.NewLineItem("D-4", 50, 1))
	fmt.Println(items.Size(), pricing.Subtotal(items))

	receipt := pricing.Checkout(items, "OFF100").Get()
	fmt.Println(receipt.TotalCents, receipt.Lines, receipt.Note)
	// Output:
	// Percent(50) 450
	// C-3 2
	// 2 1000
	// 900 2 AmountOff(100)
}
```

**Designing a GALA API for Go callers:**

- Export a `NewX(...)` function for each immutable struct Go must build. The alternative, `X{F: std.NewImmutable(v)}`, works but is noisy.
- Use `var` fields, or the block form `type X struct { var F T }`, on result types that Go reads a lot.
- Offer a Go-friendly accessor (`Describe` above) for sealed types instead of making every caller chain `Unapply` calls.
- Keep errors as Go `error` values (`errors.New`, a struct with `Error()`), so `errors.Is` and `errors.As` work on both sides.

---

## Part 3: One package, two languages

A package can hold both `.gala` and hand-written `.go` files. They are one Go package after transpilation, so each side calls the other's declarations, exported or not, without an import. Reach for this when you need:

- **Methods written in Go on a GALA type**, when their body is easier to write in Go (the `Write` below works on raw bytes).
- **A thin Go layer over an awkward Go API**: channels, `select`, `unsafe`, cgo, or code generated by another tool.

[`mixed/textstats/textstats.gala`](../examples/go_interop/mixed/textstats/textstats.gala):

<!-- doc-source: examples/go_interop/mixed/textstats/textstats.gala -->
<!-- doc-check: fragment -->
```gala
package textstats

import (
    "strings"

    . "martianoff/gala/collection_immutable"
)

// Counter is written to by Go's io functions through the Write method in
// writer.go. Its fields are `var`, so Go sees plain fields; inWord carries a
// word that one Write ends and the next continues.
struct Counter(var Lines int, var Words int, var Bytes int, var inWord bool = false)

// Stats is the immutable summary GALA code works with.
struct Stats(Lines int, Words int, Bytes int, Longest string)

// countWords is unexported GALA that writer.go calls.
func countWords(text string) int = strings.Fields(text).Size()

// longestWord is used by Summarize.
func longestWord(text string) string =
    ArrayOf(strings.Fields(text)...).FoldLeft("", (best, w) => if (w.Size() > best.Size()) w else best)

// Summarize calls CountReader, a Go function from writer.go. It returns
// (Counter, error), so GALA sees a Try[Counter].
func Summarize(text string) Try[Stats] =
    CountReader(strings.NewReader(text)).Map((c) => Stats(c.Lines, c.Words, c.Bytes, longestWord(text)))
```

[`mixed/textstats/writer.go`](../examples/go_interop/mixed/textstats/writer.go), in the same directory and the same package:

<!-- doc-source: examples/go_interop/mixed/textstats/writer.go -->
```go
package textstats

import (
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Write makes *Counter an io.Writer. A Go file in the same package can add
// a method to the GALA struct; this one works on raw bytes, which reads more
// simply in Go. A caller may split its input across several writes (io.Copy
// writes 32 KB at a time), so a word cut in two is counted once.
func (c *Counter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	text := string(p)
	c.Bytes += len(p)
	c.Lines += strings.Count(text, "\n")
	c.Words += countWords(text) // unexported GALA function, same package
	first, _ := utf8.DecodeRune(p)
	if c.inWord && !unicode.IsSpace(first) {
		c.Words-- // the previous write ended inside this word
	}
	last, _ := utf8.DecodeLastRune(p)
	c.inWord = !unicode.IsSpace(last)
	return len(p), nil
}

// CountReader copies r into a Counter with Go's io.Copy.
func CountReader(r io.Reader) (Counter, error) {
	var c Counter
	_, err := io.Copy(&c, r)
	return c, err
}

// MarshalJSON makes Stats a json.Marshaler with lowercase keys. Its fields are
// immutable, which Go sees as std.Immutable[T] values; Get reads them.
func (s Stats) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"lines":   s.Lines.Get(),
		"words":   s.Words.Get(),
		"bytes":   s.Bytes.Get(),
		"longest": s.Longest.Get(),
	})
}
```

From the outside the package is a single package. [`mixed/main.gala`](../examples/go_interop/mixed/main.gala) uses the Go-declared methods on the GALA types:

<!-- doc-source: examples/go_interop/mixed/main.gala -->
```gala
package main

import (
    "encoding/json"
    "fmt"

    "martianoff/gala/examples/go_interop/mixed/textstats"
)

func main() {
    val text = "GALA transpiles to Go\nand Go calls GALA back\n"
    textstats.Summarize(text) match {
        case Success(stats) => {
            Println(s"lines=${stats.Lines} words=${stats.Words} longest=${stats.Longest}")
            // Stats.MarshalJSON is declared in Go, so encoding/json uses it.
            Println(json.Marshal(stats).Map((b) => string(b)).GetOrElse("cannot encode"))
        }
        case Failure(err) => Println(s"failed: ${err.Error()}")
    }

    // *Counter is an io.Writer, so fmt.Fprintf can write into a GALA struct.
    var counter textstats.Counter
    fmt.Fprintf(&counter, "%d words\n", 3)
    Println(s"lines=${counter.Lines} words=${counter.Words} bytes=${counter.Bytes}")

    // A word split across two writes counts once.
    fmt.Fprint(&counter, "split wo")
    fmt.Fprint(&counter, "rd\n")
    Println(s"lines=${counter.Lines} words=${counter.Words} bytes=${counter.Bytes}")
}
```

<!-- doc-source: examples/go_interop/mixed/main.out -->
```
lines=2 words=9 longest=transpiles
{"bytes":45,"lines":2,"longest":"transpiles","words":9}
lines=1 words=2 bytes=8
lines=2 words=4 bytes=19
```

**Rules for a mixed package:**

- All files share one `package` name and one namespace, so a name declared in both a `.gala` and a `.go` file is a duplicate.
- A Go file can add methods to a GALA type, as long as the transpiler does not generate a method of that name. It generates `Copy`, `Equal`, `Unapply`, and for sealed types `String`. For an opaque type, a hand-written `Hash` or `Compare` replaces the generated one.
- Don't name a hand-written file `x.gen.go` next to an `x.gala`, or `x_test.gen_test.go` next to an `x_test.gala`: `gala build` writes the `.gala` file's output under that name and rejects the clash.

---

## Part 4: Building a mixed project

### With the `gala` CLI

`gala build`, `gala run` and `gala test` build `.gala` and `.go` files together. The project below is the `go_interop` fixture in [`tools/ci/cli_path/testdata/go_interop`](../tools/ci/cli_path/testdata/go_interop), which CI builds with a CLI built from the same sources:

```
go_interop/
├── gala.mod            module example.com/gointerop
├── go.mod
├── main.gala           GALA main: calls Go (helper.go, report) and GALA (textstats)
├── helper.go           Go in package main: calls GALA (exclaim) and Go (report)
├── textstats/          one package of GALA plus hand-written Go (Part 3)
│   ├── textstats.gala
│   └── writer.go
├── report/
│   └── report.go       plain Go importing the GALA package textstats
└── cmd/gomain/
    └── main.go         a Go main package calling GALA through report
```

- **GALA calls Go and Go calls GALA by import path.** `report/report.go` imports `example.com/gointerop/textstats`, a GALA package, like any other package of the module.
- **`.go` files next to `main.gala`** belong to package `main` too. `helper.go` calls `exclaim`, a function declared in `main.gala`, and `main.gala` calls `helper.go`'s functions.
- **A Go `main` package works too.** `gala build -o gomain ./cmd/gomain` builds a binary whose `main` is Go and which reaches GALA through its imports.
- **Third-party Go modules** go in `gala.mod` with `// go` (`gala mod add ... --go`). Use `gala mod tidy`, not `go mod tidy`.

Generated Go is written to a build workspace under `GALA_HOME`, not to your source tree. A plain `go build` does not understand `.gala` files, so build with `gala` (or Bazel).

### With Bazel

A Go library is an ordinary `deps` entry of a GALA target, and a `gala_library` is an ordinary dependency of a `go_library`, `go_binary` or `go_test`. The two wirings that need GALA's rules are a package that mixes both languages, and a GALA dependency whose constructors the code uses. From [`examples/go_interop/BUILD.bazel`](../examples/go_interop/BUILD.bazel):

<!-- doc-source: examples/go_interop/BUILD.bazel -->
```python
go_library(
    name = "checkout",
    srcs = ["checkout/checkout.go"],
    importpath = "martianoff/gala/examples/go_interop/checkout",
    deps = [
        ":pricing",
        "//collection_immutable",
        "//std",
    ],
)

gala_library(
    name = "textstats",
    srcs = ["mixed/textstats/textstats.gala"],
    go_srcs = ["mixed/textstats/writer.go"],
    importpath = "martianoff/gala/examples/go_interop/mixed/textstats",
    deps = ["//collection_immutable"],
)

gala_exec_test(
    name = "mixed",
    src = "mixed/main.gala",
    expected = "mixed/main.out",
    gala_deps = [":textstats"],
)
```

`checkout` is Go depending on the GALA library `:pricing`. `textstats` puts the hand-written Go in `go_srcs`. `mixed` lists `:textstats` in `gala_deps`, the recommended place for a GALA dependency: the transpiler then reads its source, which a call to one of its struct or sealed-case constructors needs (from `deps` alone the transpiler cannot tell such a call from a Go conversion). Gazelle (`bazel run //:gazelle`) generates these targets for GALA, Go and mixed packages. See [Bazel Integration](DEPENDENCY_MANAGEMENT.MD#8-bazel-integration) for `MODULE.bazel`, third-party Go modules and the `deps`/`gala_deps` split.

---

## Gotchas and limitations

A checklist of what differs from writing Go. The first ones are covered in the parts above and are listed here so nothing is missed.

**Calling Go from GALA**

- **A `(T, error)` call is a `Try[T]`, not a `T`**, and `(A, B)` is a Tuple ([table](#go-functions-that-return-several-results)). Using one as the plain value is [GALA-E0049](errors/GALA-E0049.md). `val (data, err) = os.ReadFile(p)` is an error too, because a `Try` is not a Tuple: `val data, err = os.ReadFile(p)`, without parentheses, gives Go's raw values. Don't wrap a `(T, error)` call in `Try(...)`. It is already a `Try`.
- **No `if` initializer.** `if n, err := strconv.Atoi(s); err != nil` is [GALA-E0047](errors/GALA-E0047.md). Use `match` or `GetOrElse`.
- **Pass `&x` of a `var` to Go**, never of a `val`, which gives a read-only `ConstPtr[T]` ([pointer receivers](#implementing-go-interfaces)).
- **No-copy Go types live in a `var`.** With `val sb = strings.Builder{}`, the call `sb.WriteString(...)` is [GALA-E0053](errors/GALA-E0053.md) because the pointer method would run on a copy. Write `var sb strings.Builder`, and the same for `bytes.Buffer`, `sync.Mutex` and the `sync/atomic` types.
- **Immutability is shallow for Go values.** A `val`, or an immutable field, that holds a Go slice, map or pointer shares it, so Go code (or an index assignment) can still change what it points to. `sort.Sort(words)` above sorts the slice inside an immutable field. Convert to `Array`/`HashMap` when the contents must not change.
- **No slice or map literals, slice expressions, bare builtins, `chan`, `select`, `go` or `defer`.** Each has a replacement ([slices and maps](#go-slices-and-maps), [Go features without GALA syntax](#go-features-that-have-no-gala-syntax)), and the compiler error names it.
- **A GALA package named like a Go package keeps the name**, so alias the Go one: `gostrings "strings"`. A Go keyword used as a name (`type`, `func`, `range`, …) is [GALA-E0055](errors/GALA-E0055.md). GALA never renames an identifier, because Go code would see the new name.
- **You cannot add a method to a Go type**, not even through an alias ([GALA-E0048](errors/GALA-E0048.md)). Wrap the value in a GALA struct, or write the method in Go inside the Go package.
- **Go type information comes from a Go SDK.** Without `go` on `PATH` (or `GOROOT`), transpiling still works, but Go result types are unknown and calls that depend on them fail to infer.

**Calling GALA from Go**

- **Immutable fields are `std.Immutable[T]`.** Go reads them with `.Get()` and cannot assign them. Go can build a struct with `std.NewImmutable(v)` per field, but an exported `NewX` constructor reads better. Generating one is a proposed extension ([#688](https://github.com/martianoff/gala/issues/688)).
- **Encoders see the value.** `encoding/json`, YAML libraries (`gopkg.in/yaml.v2`, `v3`) and `fmt` encode, decode and print an exported immutable field as the value it holds, and the field's struct tag applies to that value. To `encoding/json` the field is a struct, so `omitempty` never leaves it out; use `omitzero`. A `json.Decoder`'s `DisallowUnknownFields` and `UseNumber` do not reach inside the field.
- **Sealed cases are told apart with `Unapply`**, because the variant tag is unexported: `(pricing.Percent{}).Unapply(d).IsDefined()`. A GALA helper such as `Describe` above is friendlier.
- **Generic methods are free functions.** `opt.Map(f)` in GALA is `std.Option_Map(opt, f)` in Go, and `arr.FoldLeft(z, f)` is `collection_immutable.Array_FoldLeft(arr, z, f)`.
- **Default and named arguments are GALA-only.** Go passes every parameter, in order.
- **Lowercase means unexported**, for Go too. A camelCase GALA function is invisible to Go in other packages, and visible to `.go` files in its own package.

**Building**

- **`.go` files build together with the `.gala` files.** A plain `go build` or `go test` does not understand `.gala` sources. Build with `gala` or Bazel.
- **Don't name a hand-written file `x.gen.go` next to `x.gala`** (or `x_test.gen_test.go` next to `x_test.gala`). That is the name `gala build` gives the `.gala` file's output, so the clash is an error.
- **Bazel: a GALA dependency whose constructors you call goes in `gala_deps`** ([Part 4](#with-bazel)).
