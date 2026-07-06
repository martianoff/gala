---
layout: default
title: "GALA Go Interop — Use Any Go Library, Type, and Function"
description: "GALA transpiles to Go and gives you full access to the Go ecosystem. Import Go packages, call Go functions, use Go types — all with GALA's cleaner syntax. Zero friction interoperability."
keywords: "gala go interop, gala go libraries, transpile to go, gala import go, gala go types, gala go functions, gala go slices, gala go maps, gala go compatibility"
permalink: /features/go-interop/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/sealed-types/">Features</a> / Go Interop</p>

# Full Go Interoperability

GALA transpiles to Go. Every Go package, type, and function is available in GALA code with zero friction. Your existing Go modules, third-party libraries, and tooling all work out of the box.

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

Go functions that return `(T, error)` work naturally:

```gala
import "os"

val file, err = os.Open("data.txt")
if err != nil {
    Println(s"Error: ${err.Error()}")
}
```

Or wrap them in `Try` for monadic error handling:

```gala
import "os"
import . "martianoff/gala/std"

val result = Try(os.Getwd())
val dir = result.GetOrElse("/tmp")
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
— is consumed exactly like a stdlib error pair: as a multi-return, or wrapped in
`Try` for pattern matching:

```gala
import "github.com/google/uuid"

func describe(s string) string = Try(uuid.Parse(s)) match {
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

// Pass to any Go function expecting this struct
val jsonBytes, _ = json.Marshal(Config{Host: "localhost", Port: 8080})
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
val value, ok = MapGet(goMap, "key")
val exists = MapContains(goMap, "key")

// Iterate
MapForEach(goMap, (k string, v int) => {
    Println(s"$k: $v")
})
```

Convert between `HashMap` and Go map:

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

Go's built-in functions are available directly:

```gala
val length = len("hello")         // 5
val sliceCap = cap(mySlice)       // slice capacity
val ch = make(chan int, 10)       // buffered channel

// Println and Print are available without importing fmt
Println("hello world")
Print("no newline")
```

---

## Further Reading

- [Collections](/features/collections/) — immutable Array, List, HashMap, and more
- [Error Handling](/features/error-handling/) — Option, Either, and Try for Go error handling
- [Concurrency](/features/concurrency/) — channels, goroutines, and Future in GALA
