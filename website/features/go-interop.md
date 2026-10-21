---
layout: default
title: "GALA Go Interop — Use Any Go Library, Type, and Function"
description: "GALA transpiles to Go and gives you full access to the Go ecosystem. Import Go packages, call Go functions, use Go types — all with GALA's cleaner syntax. Zero friction interoperability."
keywords: "gala go interop, gala go libraries, transpile to go, gala import go, gala go types, gala go functions, gala go slices, gala go maps, gala go compatibility"
permalink: /features/go-interop/
last_modified_at: 2026-10-20
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Go Interop</p>

# Full Go Interoperability

GALA transpiles to Go. Every Go package, type, and function is available in GALA code with zero friction. Your existing Go modules, third-party libraries, and tooling all work out of the box.

This page covers calling Go from GALA, then [the other direction](#calling-gala-from-go) in brief. For packages that mix `.gala` and `.go` files, project layout, building and the gotchas, see the **[Go Interop Guide](/docs/go-interop/)**.

---

## Importing Go Packages

Standard Go imports work directly in GALA:

```gala
import "strings"
import "fmt"
import "net/http"
import "encoding/json"
```

Grouped imports are supported:

```gala
import (
    "strings"
    "net/http"
    "os"
)
```

Aliased and dot imports work too:

```gala
import mystrings "strings"
import . "martianoff/gala/std"
```

---

## Calling Go Functions

Call any Go function with GALA's syntax:

```gala
import "strings"

val upper = strings.ToUpper("hello")        // "HELLO"
val parts = strings.Split("a,b,c", ",")     // []string{"a","b","c"}
val contains = strings.Contains("hello", "ell")  // true
```

### Go functions that return several results

Many Go functions return more than one result — usually a value plus an
`error` that says whether the call worked. In GALA every expression is one
value, so such a call becomes one GALA value automatically:

| The Go function returns | In GALA the call is a | Example | Its GALA value |
|---|---|---|---|
| a value and an error `(T, error)` | `Try[T]` | `strconv.Atoi(s)` | `Try[int]` |
| several values and an error | `Try` of a Tuple | `net.SplitHostPort(addr)` | `Try[Tuple[string, string]]` |
| two or more values | a Tuple | `strings.Cut(s, "=")` | `Tuple3[string, string, bool]` |
| only an `error` | the `error` itself | `os.Remove(path)` | `error` (nil when it worked) |

A `Try` is `Success(value)` when the call worked and `Failure(err)` when it
returned an error, so you match on it or use its methods directly:

```gala
import (
    "os"
    "strconv"
    "strings"
)

val port = strconv.Atoi("8080") match {
    case Success(n) => n
    case Failure(_) => 80
}
val dir = os.Getwd().GetOrElse("/tmp")
val (key, value, found) = strings.Cut("mode=fast", "=")
```

Using the `Try` where the plain value is expected — `val n = strconv.Atoi(s)`
and then `n + 1` — is a compile-time error,
[GALA-E0049](/docs/errors/gala-e0049/). Take the value with `.Get()` (panics
on failure), `.GetOrElse(default)`, or a `match`.

A Go function that returns only an `error` gives you that error. Wrap the call
in `Try(...)` to turn a non-nil error into a `Failure`: `Try(os.Remove(path))`
is a `Try[Void]`. Around a `(T, error)` call, `Try(...)` gives the same
`Try[T]` and additionally catches a panic.

To get Go's results one by one — for example to hand them straight back to
other Go code — bind several names:

```gala
import "os"

val file, err = os.Open("data.txt")   // file *os.File, err error
```

---

## Third-Party Go Modules

Third-party modules work exactly like the standard library — not just stdlib.
Add the module as a Go dependency, import it, and call it directly. GALA reads
the module's source to infer concrete types (return types, struct fields,
methods), so you get `uuid.UUID`, never `any`.

Add the dependency with `--go` so GALA tracks it as a Go (not GALA) package:

```bash
gala mod add github.com/google/uuid@v1.6.0 --go
```

Then use it like any other package:

```gala
import "github.com/google/uuid"

func main() {
    val id = uuid.New()
    Println(s"id: ${id.String()}")
}
```

A function returning `(T, error)` — like `uuid.Parse(string) (uuid.UUID, error)`
— works exactly like a standard-library one: the call is a `Try[uuid.UUID]`
you can match on directly:

```gala
import "github.com/google/uuid"

func describe(s string) string = uuid.Parse(s) match {
    case Success(id) => s"valid: ${id.String()}"
    case Failure(_)  => s"invalid: $s"
}
```

Both the plain CLI (`gala build` / `gala run`) and Bazel builds resolve these
types from the dependency's source. See
[Dependency Management](/docs/dependency-management/) for adding and pinning Go
modules.

---

## Using Go Types

Define and use Go struct types with GALA syntax:

```gala
import "net/http"

// Go struct types work in GALA
val client = &http.Client{}
```

GALA structs are Go structs under the hood, so they interoperate seamlessly:

```gala
import "encoding/json"

type Config struct {
    var Host string
    var Port int
}

// Pass to any Go function expecting this struct; a (T, error) result comes back as a Try
val jsonBytes = json.Marshal(Config(Host = "localhost", Port = 8080))   // Try[[]byte]
```

---

## Type Conversions

GALA supports Go-style type conversions:

```gala
// Numeric conversions
val n = int64(42)
val f = float64(10)
val pi = 3.14
val i = int(pi)             // truncates to 3

// Rune/string conversions
val r = rune(65)            // int to rune: 'A'
val s = string(r)           // rune to string: "A"
```

For byte and rune slice conversions, use the `go_interop` helpers:

```gala
import . "martianoff/gala/go_interop"

val bytes = ToBytes("hello")   // string to []byte
val str = ToString(bytes)      // []byte to string
val runes = ToRunes("hello")   // string to []rune
```

---

## Slices: Go `[]T` Interop

GALA collections (`Array`, `List`) are preferred for general programming. When you need Go's native `[]T` — for passing data to Go libraries or variadic arguments — use the `go_interop` package:

```gala
import . "martianoff/gala/go_interop"

val goSlice = SliceOf(1, 2, 3, 4, 5)    // Go []int
val empty = SliceEmpty[int]()            // Go []int{}
val withCap = SliceWithCapacity[int](10) // []int with capacity 10
```

Convert between GALA collections and Go slices:

```gala
import . "martianoff/gala/collection_immutable"
import . "martianoff/gala/go_interop"

// GALA Array → Go slice
val arr = ArrayOf(1, 2, 3)
val goSlice = arr.ToGoSlice()

// Go slice → GALA Array
val backToArray = ArrayFromSlice(goSlice)
```

### When to Use What

| Use Case | Recommendation |
|----------|---------------|
| General programming | <code>Array</code> or <code>List</code> from <code>collection_immutable</code> |
| Need Map/Filter/FoldLeft | <code>Array</code> or <code>List</code> (full functional API) |
| Passing data to Go libraries | <code>SliceOf</code> from <code>go_interop</code>, or <code>.ToGoSlice()</code> |
| Variadic function arguments | Go slices with <code>SliceOf</code> |

### Available Slice Functions

| Function | Description |
|----------|-------------|
| <code>SliceOf(elements...)</code> | Create Go slice from values |
| <code>SliceEmpty[T]()</code> | Create empty Go slice |
| <code>SliceWithCapacity[T](cap)</code> | Empty slice with capacity |
| <code>SliceCopy(slice)</code> | Copy a slice |
| <code>SliceClip(s)</code> | Drop unused capacity, so appending always reallocates |
| <code>SliceSortStable(s, cmp)</code> | Stable in-place sort; <code>cmp(i, j)</code> compares <code>s[i]</code> and <code>s[j]</code> |
| <code>SliceSortedStable(s, cmp)</code> | Stably sorted copy, leaving <code>s</code> unchanged |
| <code>SliceAppendAll(dst, src)</code> | Append all elements |
| <code>SlicePrepend(s, value)</code> | Insert at front |
| <code>SliceTake(s, n)</code> | Take first n elements |
| <code>SliceDrop(s, n)</code> | Drop first n elements |

---

## Maps: Go `map[K]V` Interop

GALA's `HashMap` is preferred for most use cases. When you need Go-native `map[K]V` for interoperability:

```gala
import . "martianoff/gala/go_interop"

// Create and populate
var goMap = MapEmpty[string, int]()
goMap = MapPut(goMap, "key", 42)

// Query
val (value, ok) = MapGet(goMap, "key")
val exists = MapContains(goMap, "key")

// Iterate
MapForEach(goMap, (k string, v int) => {
    Println(s"$k: $v")
})
```

Convert between `HashMap` and Go map:

<!-- doc-check: fragment -->
```gala
import "martianoff/gala/collection_immutable"

val hashMap = collection_immutable.HashMapFromGoMap(goMap)
val backToGoMap = hashMap.ToGoMap()
```

### Available Map Functions

| Function | Description |
|----------|-------------|
| <code>MapEmpty[K, V]()</code> | Create empty map |
| <code>MapPut(m, k, v)</code> | Add/update entry, returns map |
| <code>MapGet(m, k)</code> | Get value and existence flag |
| <code>MapContains(m, k)</code> | Check if key exists |
| <code>MapDelete(m, k)</code> | Remove key, returns map |
| <code>MapLen(m)</code> | Number of entries |
| <code>MapForEach(m, f)</code> | Iterate all entries |

---

## Go Built-in Functions

Bare Go builtins such as `len`, `cap`, `make` and `panic` are **not** part of GALA's surface — calling one is [GALA-E0035](/docs/errors/gala-e0035/). Each has a GALA form or a `go_interop` wrapper:

```gala
import "martianoff/gala/go_interop"

val length = "hello".Size()                    // 5 — characters; .ByteSize() for bytes
val mySlice = go_interop.SliceOf(1, 2, 3)
val sliceCap = go_interop.SliceCap(mySlice)    // slice capacity

// Println and Print are available without importing fmt
Println("hello world")
Print("no newline")
```

The full table of builtins and their replacements is in the [language reference](/docs/language-reference/#11-go-built-in-functions).

---

## Calling GALA from Go

A GALA package is a Go package after transpilation, so Go code imports it by its import path. The generated Go is shaped for Go callers.

### Implementing Go interfaces

A GALA type implements a Go interface by having its methods. A method that Go calls with several results, such as `io.Writer`'s `Write([]byte) (int, error)`, declares Go's result list and gives the results as one GALA value: a `Try` for `(T, error)`, a Tuple for `(A, B)`. An **interface literal** implements an interface inline, one function per method, with no struct:

```gala
package main

import "fmt"

struct WriteCounter(var Writes int)

// io.Writer: the method declares Go's result list and gives it as one Try.
func (c Ptr[WriteCounter]) Write(p []byte) (int, error) {
    c.Writes = c.Writes + 1
    Success(p.Size())
}

func main() {
    var counter = WriteCounter(0)
    fmt.Fprintln(&counter, "first")
    fmt.Fprintln(&counter, "second")
    Println(counter.Writes)   // 2

    // An interface literal implements a Go interface inline.
    val greeting = fmt.Stringer(String = () => "hello")
    Println(s"$greeting, ${greeting.String()}")    // hello, hello
}
```

A lambda passed where Go expects a `func() (T, error)` callback gives a `Try` the same way. See [Returning several results to Go](/docs/language-reference/#returning-several-results-to-go) and [Interface Literals](/docs/language-reference/#interface-literals).

### Generated constructors and case predicates

Go can build a struct with a composite literal, which skips the field defaults GALA declares. So an exported shorthand struct whose fields are all exported gets a `NewT(...)` constructor, an exported sealed type gets `NewT()` for its default case and `NewT<Case>(...)` for each case, and a field with a default becomes a `With...(v)` option. Each exported case also gets an `Is<Case>()` predicate. Given this GALA package:

<!-- doc-source: examples/go_interop/pricing/pricing.gala -->
```gala
struct LineItem(SKU string, UnitCents int64, Qty int = 1)
sealed type Discount {
    default case NoDiscount()
    case Percent(Pct int64 = 10)
    case AmountOff(Cents int64)
}
```

Go code calls it like this (from a Go example test that CI runs):

<!-- doc-source: examples/go_interop/checkout/checkout_test.go -->
```go
discount := pricing.NewDiscountPercent(pricing.WithDiscountPercentPct(50))
fmt.Println(discount, discount.Apply(900))
fmt.Println(pricing.NewDiscountPercent(), pricing.NewDiscount())
item := pricing.NewLineItem("C-3", 475, pricing.WithLineItemQty(2))
fmt.Println(item.SKU.Get(), item.Qty.Get())
// Output:
// Percent(50) 450
// Percent(10) NoDiscount()
// C-3 2
```

A sealed value's case is tested with its predicate, `d.IsPercent()`, and its fields are read with the case's `Unapply`. Doc comments on GALA declarations are carried into the generated Go, so `go doc`, gopls and pkg.go.dev show them, along with notes on the generated constructors.

### Plain Go modules: `go.gala.fyi/stdlib` and `gala export`

`go build` does not transpile `.gala` files, so a plain Go module needs the generated Go. Every GALA release publishes the standard library as the Go module `go.gala.fyi/stdlib` at the same version, and `gala export` writes a GALA project as an ordinary Go module that requires it:

```sh
go get go.gala.fyi/stdlib@latest   # GALA's collections, Option, Try for Go code
gala export --out ../shapes-go     # a GALA library as a plain Go module
```

Publish the exported directory as a module, and Go programs use the library with `go get`, with no GALA toolchain installed. Exporting a project that imports another GALA module is not supported yet. The [Go Interop Guide](/docs/go-interop/#part-5-using-gala-from-a-plain-go-module) covers versions, publishing, and `gala transpile --stdlib-module`.

---

## Further Reading

- [Go Interop Guide](/docs/go-interop/) — mixing GALA and Go in both directions: calling GALA from Go, packages with both `.gala` and `.go` files, building with `gala build` and Bazel, and the gotchas
- [Collections](/features/collections/) — immutable Array, List, HashMap, and more
- [Error Handling](/features/error-handling/) — Option, Either, and Try for Go error handling
- [Concurrency](/features/concurrency/) — channels, goroutines, and Future in GALA
