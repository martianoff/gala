---
layout: default
title: "Golang Sum Types — Sealed Types and Algebraic Data Types for Go"
description: "GALA brings sum types to Go with sealed types. Define closed type hierarchies, get exhaustive pattern matching, auto-generated constructors, and compile-time safety — the #1 most-requested Go feature, available today."
keywords: "golang sum types, go sum types, go algebraic data types, golang enum types, go sealed types, go discriminated union, golang ADT, go closed interface, golang variant types, gala sealed types"
permalink: /features/sealed-types/
last_modified_at: 2026-07-05
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Sealed Types</p>

# Golang Sum Types — Sealed Types and Algebraic Data Types for Go

Sum types are the [#1 most-requested feature](https://go.dev/blog/survey2024-h1-results) in the Go Developer Survey, yet Go 1.25 still doesn't have them. GALA delivers sum types today through **sealed types** — algebraic data types (ADTs) that define a fixed set of variants, with the compiler enforcing that every match expression handles all of them. If you've been looking for golang sum types, discriminated unions, or closed type hierarchies, this is the answer.

---

## What Are Algebraic Data Types?

An algebraic data type (ADT) is a composite type formed by combining other types. The most common form is the *sum type* — a type that can be one of several named variants, each carrying different data. Languages like Rust (`enum`), Scala (`sealed trait`), and Haskell (`data`) have had these for years. Go has no native equivalent. GALA fills that gap.

With sealed types you get:

- **Closed hierarchies** — no variant can be added outside the declaration
- **Exhaustive pattern matching** — the compiler rejects incomplete matches
- **Auto-generated constructors** — companion objects with `Apply` methods
- **Auto-generated extractors** — `Unapply` methods for pattern matching

---

## Basic Sealed Type Syntax

A sealed type declaration lists every variant inside a single block. Each `case` defines a variant with its own fields:

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}
```

Variants can carry zero or more fields. `Point()` above has no fields — it represents a singleton-like value.

---

## What Gets Generated

When you write a sealed type, the transpiler produces several things automatically.

**Parent struct** — A single `Shape` struct that merges all variant fields plus a `_variant` discriminator. This keeps memory layout flat and allocation-free.

**Companion objects** — Empty structs `Circle{}`, `Rectangle{}`, `Point{}` that act as factory namespaces.

**Apply methods** — Each companion gets an `Apply` method so you can construct variants with function-call syntax:

```gala
val c = Circle(3.14)
val r = Rectangle(10.0, 20.0)
val p = Point()
```

**Unapply methods** — Each companion gets an `Unapply` method for use in pattern matching. You never need to call these directly; the `match` expression uses them behind the scenes. To test which variant a value is, `match` on it (see [Pattern Matching](pattern-matching.md)) — that's the idiomatic approach, and it stays exhaustive.

**Copy and Equal** — Like all GALA structs, sealed types get `Copy()` and `Equal()` for free.

---

## Construction Examples

Variants are constructed by calling the companion name as a function. Positional and named arguments both work:

```gala
// Positional arguments
val c = Circle(3.14)
val r = Rectangle(10.0, 20.0)

// Named arguments
val r2 = Rectangle(Width = 15.0, Height = 25.0)
```

---

## Generic Sealed Types

Sealed types support type parameters, enabling you to define reusable algebraic structures:

```gala
sealed type Result[T any] {
    case Ok(Value T)
    case Err(Error error)
}

val success = Ok(42)
val failure = Err[int](fmt.Errorf("oops"))
```

Generic sealed types work exactly like non-generic ones — you get companion objects, `Apply`/`Unapply`, and exhaustive matching, all parameterized by the type argument.

---

## Exhaustive Pattern Matching with Sealed Types

The compiler knows every variant of a sealed type. When you match on all of them, no `case _` default is needed:

```gala
func area(s Shape) string = s match {
    case Circle(r)       => f"circle area: ${3.14159 * r * r}%.2f"
    case Rectangle(w, h) => f"rect area: ${w * h}%.2f"
    case Point()         => "point"
}
```

If you forget a variant, the compiler tells you. This eliminates an entire class of bugs where new variants are added but forgotten in downstream logic.

### Pattern Matching with Guards

You can add `if` conditions to sealed type match branches:

```gala
val desc = shape match {
    case Circle(r) if r > 100.0 => "large circle"
    case Circle(r)              => s"circle with radius $r"
    case Rectangle(w, h)       => f"$w%.0f x $h%.0f"
    case Point()               => "point"
}
```

---

## Wildcard Catch-All

When you only care about a subset of variants, use `case _` as a catch-all. The compiler accepts this as a valid exhaustive match:

```gala
sealed type Animal {
    case Dog(Name string)
    case Cat(Name string)
    case Bird(Name string)
}

val msg = animal match {
    case Dog(name) => "Woof! I'm " + name
    case _         => "I'm not a dog"
}
```

---

## Standard Library Sealed Types

GALA's standard library defines three core sealed types that you will use constantly:

| Type | Variants | Purpose |
|------|----------|---------|
| `Option[T]` | `Some(Value T)`, `None()` | Optional values — eliminates nil |
| `Either[A, B]` | `Left(LeftValue A)`, `Right(RightValue B)` | Disjoint union — typed errors |
| `Try[T]` | `Success(Value T)`, `Failure(Err error)` | Failable computations |

All three support `Map`, `FlatMap`, `GetOrElse`, pattern matching, and more. See the [Error Handling](/features/error-handling/) page for details.

```gala
val opt = Some(42)
val msg = opt match {
    case Some(v) => s"got $v"
    case None()  => "empty"
}
```

---

## When to Use Sealed Types vs Interfaces

| Use sealed types when... | Use interfaces when... |
|--------------------------|------------------------|
| You know all variants at compile time | Third parties need to add implementations |
| You want exhaustive matching | You want open extension |
| Variants carry different data shapes | Implementations share a common method set |
| You are modeling state machines, ASTs, or messages | You are defining contracts between packages |

Sealed types and interfaces can coexist. You can define methods on a sealed type that satisfy an interface, giving you both closed variants and polymorphism.

---

## Further Reading

- [Pattern Matching](/features/pattern-matching/) — all match expression features, including struct destructuring and custom extractors
- [Error Handling](/features/error-handling/) — Option, Either, and Try in depth
- [Immutability](/features/immutability/) — how GALA keeps data safe by default
