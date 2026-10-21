---
layout: default
title: "Golang Immutable Structs — Immutability by Default for Go"
description: "GALA makes immutability the default in Go. val bindings, immutable struct fields, auto-generated Copy with named args, structural Equal for free, and no address for a val — eliminating mutation bugs."
keywords: "golang immutable struct, go immutable, golang immutability, go const pointer, go immutable fields, golang val var, golang immutable by default, go copy struct, go structural equality, gala immutability"
permalink: /features/immutability/
last_modified_at: 2026-10-20
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Immutability</p>

# Golang Immutable Structs — Immutability by Default for Go

Mutation is the single largest source of bugs in concurrent and large-scale programs. Race conditions, unexpected side effects, defensive copying — all stem from mutable state. Go has `const` for compile-time constants, but nothing to prevent runtime mutation of variables, struct fields, or pointed-to data. GALA eliminates these problems by making **golang immutable structs** the default — at every level: variable bindings, struct fields, and even pointers.

---

## Why Immutability Matters

- **Concurrency safety** — immutable data cannot race. No mutexes needed.
- **Easier reasoning** — a value that never changes is a value you can trust.
- **Fewer bugs** — entire categories of defects (stale references, aliasing bugs, iterator invalidation) disappear.
- **Better APIs** — callers know a function cannot modify their data.

Go has `const` for compile-time constants, but nothing to prevent runtime mutation of variables, struct fields, or pointed-to data. GALA fills that gap.

---

## val vs var

GALA distinguishes immutable and mutable bindings at declaration:

```gala
val x = 10      // immutable — cannot be reassigned
var y = 20      // mutable — can be reassigned
y = 30          // OK
// x = 20       // compile error: cannot assign to immutable variable
```

Multiple declarations work too:

```gala
val a, b = 1, 2
var x, y int = 10, 20
```

### Short Declarations Are Immutable

The `:=` operator creates an **immutable** binding, just like `val`:

```gala
func main() {
    z := 40
    // z = 50   // compile error: cannot assign to immutable variable
}
```

This is a deliberate departure from Go, where `:=` creates a mutable variable. In GALA, you must explicitly opt in to mutation with `var`.

**Exception:** Loop variables declared with `:=` in a `for` init statement are mutable so that `i++` works:

```gala
for i := 0; i < 10; i++ {
    Println(i)    // i is mutable here
}
```

---

## Immutable Struct Fields

Struct fields are immutable by default. Use `var` to opt in to mutability:

### Shorthand Syntax

```gala
struct Person(Name string, Age int, var Score int)
// Name and Age are immutable
// Score is mutable
```

### Block Syntax

```gala
type Config struct {
    Host string       // immutable
    Port int          // immutable
    var MaxConns int  // mutable
}
```

Attempting to assign to an immutable field produces a compile error. This is enforced at the transpiler level — the generated Go code wraps immutable fields in `Immutable[T]`, a container with a `Get()` method but no `Set()`.

---

## Copy() with Named Arguments

Every GALA struct gets a `Copy()` method for free. It creates a new instance with optional field overrides:

```gala
struct Config(Host string, Port int)

val base = Config("localhost", 3000)
val updated = base.Copy(Port = 8080)
// updated is Config("localhost", 8080)
// base is unchanged
```

The idiomatic Go equivalent copies the struct value, then mutates the field in place:

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

<pre><code class="language-gala">struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)</code></pre>

</td>
<td>

<pre><code class="language-go">type Config struct {
    Host string
    Port int
}
updated := config
updated.Port = 8080</code></pre>

</td>
</tr>
</table>

Go's copy is concise, but `updated` and `config` both stay mutable, and if `Config` holds a slice or map the value copy shares it. GALA's `Copy()` returns a new **immutable** value as an expression -- no temp variable, no in-place mutation, and the original `val` can never be reassigned. It also composes: you can drop `config.Copy(Port = 8080)` straight into a function call or pipeline.

---

## Equal() — Structural Equality for Free

Every GALA struct also gets an `Equal()` method that performs deep structural comparison of all fields:

```gala
struct Person(Name string, Age int)

val p1 = Person("Alice", 30)
val p2 = Person("Alice", 30)
val same = p1.Equal(p2)    // true
```

No need to implement equality by hand. No risk of forgetting a field.

---

## A `val` Has No Address

`&x` is a pointer, and a pointer can write. So a `val`, a parameter, a receiver and an immutable field have no address: taking one is [GALA-E0076](/docs/errors/gala-e0076/). To read a value without changing it, pass the value itself; it is already immutable.

<!-- doc-check: error GALA-E0076 -->
```gala
val data = 42
val ptr = &data       // error: GALA-E0076
```

The address of a `var` can be taken, and writes go through it:

```gala
package main

func main() {
    var data = 42
    val ptr = &data       // Ptr[int]: never nil, an *int in the generated Go
    *ptr = 100            // writes data through the pointer
    Println(data)         // 100
}
```

---

## Pointers That Are Never Nil: `Ptr[T]` and `OptionPtr[T]`

GALA has three pointer types:

| Type | Can be nil | Use |
|------|------------|-----|
| `Ptr[T]` | never | a pointer created and used in GALA code; `&x` is one |
| `OptionPtr[T]` | it is empty instead | a pointer that may be absent |
| `*T` | yes | Go interop: what Go functions take and return |

`Ptr[T]` is checked at compile time and compiles to a plain Go `*T`, so it costs nothing at run time. It has the fields and methods of what it points to, can be a method receiver, and passes to Go wherever a `*T` is expected. The other direction is checked: `nil`, or a Go `*T` that may be nil, never fills a `Ptr`, and comparing a `Ptr` with nil is an error ([GALA-E0080](/docs/errors/gala-e0080/)).

```gala
package main

struct Counter(var N int)

func (c Ptr[Counter]) Bump() {
    c.N = c.N + 1
}

func main() {
    var c = Counter(0)
    val p = &c      // Ptr[Counter]: never nil; *Counter in the generated Go
    p.Bump()
    p.Bump()
    Println(c.N)    // 2
}
```

`OptionPtr[T]` is a pointer that may be absent, the size of one pointer. It matches as `Some(p)` / `None()`, with `p` a `Ptr[T]`, and a Go pointer is converted once, where it enters GALA code: `OptionPtrOf(p)` when nil means absent, `RequirePtr(p)` when nil is a bug.

```gala
package main

import "flag"

struct Node(Value int, Next OptionPtr[Node])

func sum(n Ptr[Node]) int = n.Next match {
    case Some(next) => n.Value + sum(next)
    case None()     => n.Value
}

func main() {
    val list = &Node(1, SomePtr(&Node(2, SomePtr(&Node(3, EmptyOptionPtr())))))
    Println(sum(list))        // 6

    // flag.Lookup returns a Go *flag.Flag that is nil when the flag is unknown
    val port = OptionPtrOf(flag.Lookup("port"))
    Println(port.IsEmpty())   // true
}
```

Exported functions check their `Ptr` parameters for nil on entry, so a nil passed from Go code is caught at the boundary. The full rules are in the [language reference](/docs/language-reference/#non-null-pointers-ptrt-and-optionptrt).

---

## When to Use var

Immutability is the default, but mutation is sometimes the right choice:

**Mutable collections** — When you need in-place updates for performance:

```gala
import . "martianoff/gala/collection_mutable"

val m = EmptyHashMap[string, int]()
m.Put("a", 1)    // modifies in place
m.Put("b", 2)
```

**Accumulators** — A running total is the classic reason to reach for `var` in Go. In GALA, a fold usually carries the running value for you, so check for one before declaring a `var` and a loop:

<!-- doc-check: fragment -->
```gala
val total = items.FoldLeft(0, (acc, v) => acc + v)
```

**Pointer fields in linked structures** — When a link is set after construction:

```gala
struct Node(Value int, var Next OptionPtr[Node])   // var: the link is set after construction
```

The rule of thumb: start with `val` and immutable fields. Switch to `var` only when you have a concrete reason.

---

## Immutability at Every Level

GALA enforces immutability at three levels:

| Level | Immutable (default) | Mutable (opt-in) |
|-------|--------------------|--------------------|
| Variable bindings | `val x = 10`, `x := 10` | `var x = 10` |
| Struct fields | `Name string` | `var Name string` |
| Pointers | no address for a `val` (`&val` is an error) | `Ptr[T]` (from `&var`, never nil) |

This layered approach means immutability is not just a convention — it is enforced by the compiler at every point where data flows through your program.

---

## Further Reading

- [Sealed Types](/features/sealed-types/) — immutable algebraic data types with exhaustive matching
- [Error Handling](/features/error-handling/) — Option, Either, and Try for safe error propagation
- [Pattern Matching](/features/pattern-matching/) — structural destructuring and guards
