---
layout: default
title: "Golang Immutable Structs — Immutability by Default for Go"
description: "GALA makes immutability the default in Go. val bindings, immutable struct fields, auto-generated Copy with named args, structural Equal for free, and ConstPtr read-only pointers — eliminating mutation bugs."
keywords: "golang immutable struct, go immutable, golang immutability, go const pointer, go immutable fields, golang val var, golang immutable by default, go copy struct, go structural equality, gala immutability"
permalink: /features/immutability/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/immutability/">Features</a> / Immutability</p>

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

This replaces the tedious Go pattern of manually copying every field:

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

```gala
struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)
```

</td>
<td>

```go
type Config struct {
    Host string
    Port int
}
updated := Config{Host: config.Host, Port: 8080}
```

</td>
</tr>
</table>

With two fields the Go version is manageable. With ten fields, `Copy()` saves significant boilerplate and prevents bugs from forgetting to copy a field.

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

## ConstPtr[T] — Read-Only Pointers

When you take the address of a `val` variable, GALA gives you a `ConstPtr[T]` instead of a raw `*T`. A `ConstPtr` allows reading but prevents writing through the pointer:

```gala
val data = 42
val ptr = &data       // ConstPtr[int], not *int
val value = *ptr      // OK: read — returns 42
// *ptr = 100         // compile error: cannot write through ConstPtr
```

### Field Access Through ConstPtr

The transpiler auto-dereferences `ConstPtr` for field access:

```gala
struct Person(Name string, Age int)

val alice = Person("Alice", 30)
val ptr = &alice

Println(ptr.Name)     // "Alice" — auto-deref, no explicit Deref() needed
Println(ptr.Age)      // 30
```

### ConstPtr in Struct Fields

`ConstPtr` is useful for struct fields that hold references to shared, immutable data:

```gala
struct Team(Leader ConstPtr[Person], MemberCount int)

val alice = Person("Alice", 30)
val team = Team(Leader = &alice, MemberCount = 5)
Println(team.Leader.Name)    // "Alice" — auto-deref through ConstPtr
```

### Mutable Pointers

When you take the address of a `var` variable, you get a regular `*T` that supports both reading and writing:

```gala
var data = 42
val ptr = &data       // *int (regular pointer)
*ptr = 100            // OK: can modify data through pointer
```

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

**Accumulators in loops** — When building up a value iteratively:

```gala
var total = 0
for _, v := range items {
    total = total + v
}
```

**Pointer fields in linked structures** — When a pointer must be reassigned:

```gala
type Node struct {
    Value int
    var Next *Node    // must be var to allow traversal/modification
}
```

The rule of thumb: start with `val` and immutable fields. Switch to `var` only when you have a concrete reason.

---

## Immutability at Every Level

GALA enforces immutability at three levels:

| Level | Immutable (default) | Mutable (opt-in) |
|-------|--------------------|--------------------|
| Variable bindings | `val x = 10`, `x := 10` | `var x = 10` |
| Struct fields | `Name string` | `var Name string` |
| Pointers | `ConstPtr[T]` (from `&val`) | `*T` (from `&var`) |

This layered approach means immutability is not just a convention — it is enforced by the compiler at every point where data flows through your program.

---

## Further Reading

- [Sealed Types](/features/sealed-types/) — immutable algebraic data types with exhaustive matching
- [Error Handling](/features/error-handling/) — Option, Either, and Try for safe error propagation
- [Pattern Matching](/features/pattern-matching/) — structural destructuring and guards
