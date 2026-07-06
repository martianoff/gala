---
layout: default
title: "Golang Pattern Matching — Exhaustive Match Expressions for Go"
description: "GALA brings real pattern matching to Go — struct destructuring, sealed type exhaustive matching, guard clauses, nested patterns, custom extractors, and sequence patterns. Beyond what Go's switch can do."
keywords: "golang pattern matching, go pattern matching, golang match expression, go switch alternative, go exhaustive match, go destructuring, golang guard clauses, go struct pattern matching, golang case expression"
permalink: /features/pattern-matching/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/pattern-matching/">Features</a> / Pattern Matching</p>

# Golang Pattern Matching — Beyond Go's Switch Statement

Go's `switch` is limited to value comparison. Runtime pattern matching libraries like [go-pattern-match](https://github.com/alexpantyukhin/go-pattern-match) try to fill the gap but lack compile-time safety and clean syntax. GALA's `match` expression gives you **structural destructuring, exhaustive checking, guard clauses, nested patterns, custom extractors, and sequence matching** — all as expressions that return values, with errors caught at compile time.

---

## Overview

The `match` expression takes a subject and tests it against a series of `case` branches. The first matching branch wins, and its body becomes the result. A `case _` default is required unless the match is provably exhaustive (sealed types with all variants covered, or booleans with both `true` and `false`).

```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case _ => "other"
}
```

Every match branch is an expression. You can assign the result directly to a `val`, pass it to a function, or use it inline.

---

## Literal and Variable Binding

Match against literal values or bind the subject to a new variable:

```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => s"Value is $n"   // n is bound to x
    case _ => "other"
}
```

**Unused variable rule:** All variables extracted in match patterns must be referenced in the branch body or guard. Use `_` to discard values you do not need:

```gala
// ERROR: unused variable 'y' in match branch
case Some(y) => "has value"

// OK: use '_' to discard
case Some(_) => "has value"

// OK: variable is used in body
case Some(y) => s"got $y"

// OK: variable is used in guard
case Some(y) if y > 0 => "positive"
```

---

## Struct Destructuring

Structs with public fields automatically generate `Unapply` methods, enabling positional extraction in match patterns:

```gala
struct Person(Name string, Age int)

val people = ArrayOf(
    Person("Alice", 25),
    Person("Bob", 15),
    Person("Charlie", 70),
)

people.ForEach((p) => {
    val status = p match {
        case Person(name, age) if age < 18 => name + " is a minor"
        case Person(name, age) if age > 65 => name + " is a senior"
        case Person(name, _)               => name + " is an adult"
        case _                             => "Unknown"
    }
    Println(status)
})
```

You can match on specific field values and ignore others with `_`:

```gala
val msg = p match {
    case Person(name, 30) => name + " is 30"
    case Person(_, age)   => s"Someone is $age"
    case _                => "Unknown"
}
```

---

## Sealed Type Matching (Exhaustive)

When matching on a [sealed type](/features/sealed-types/), the compiler knows every variant. If you cover them all, no `case _` is required:

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

func describe(s Shape) string = s match {
    case Circle(r)       => f"circle r=$r%.2f"
    case Rectangle(w, h) => f"rect $w%.0f x $h%.0f"
    case Point()         => "point"
}
```

Forget a variant and the compiler rejects your code.

---

## Guard Clauses

Add `if` conditions after a pattern to refine the match. Guards have access to all variables extracted by the pattern:

```gala
val res = x match {
    case i: int if i > 100 => "Large integer"
    case i: int if i > 0   => "Positive integer"
    case Person(name, age) if age < 18 => name + " is a minor"
    case _ => "Other"
}
```

Guards are evaluated after the pattern matches. If the guard fails, the next `case` is tried.

---

## Type-Based Matching

Match on the runtime type of a value. This is useful with `any` or interface types:

```gala
val x any = "hello"

val res = x match {
    case s: string => s"string: $s"
    case i: int    => s"int: $i"
    case _         => "unknown"
}
```

### Generic Type Matching

Match against specific instantiations of generic types:

```gala
type Wrap[T any] struct { Value T }

val w = Wrap[int](Value = 42)
val res = w match {
    case w: Wrap[int]    => s"Wrapped int: ${w.Value}"
    case w: Wrap[string] => "Wrapped string: " + w.Value
    case _               => "Other"
}
```

### Wildcard Generic Type Matching

Use `[_]` to match any instantiation of a generic type:

```gala
type Wrap[T any] struct { Value T }
func (w Wrap[T]) GetValue() any = w.Value

val w = Wrap[string](Value = "hello")
val res = w match {
    case w1: Wrap[_] => s"Matched Wrap[_]: ${w1.GetValue()}"
    case _           => "Other"
}
```

---

## Nested Patterns

Patterns compose. You can nest extractors inside extractors for deep matching:

```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None()

val opt = Some(10)
opt match {
    case Some(Even(n)) => Println("Found some even number", n)
    case Some(n)       => Println("Found some odd number", n)
    case None()        => Println("Nothing found")
    case _             => Println("Other")
}
```

Nested patterns work to arbitrary depth. The transpiler chains the `Unapply` calls automatically.

---

## Sequence Patterns

GALA supports Scala-like sequence pattern matching for collections that implement the `Seq` interface (such as `Array` and `List` from `collection_immutable`). Use `...` to match remaining elements:

```gala
import . "martianoff/gala/collection_immutable"

val arr = ArrayOf(1, 2, 3, 4, 5)

// Extract head and capture tail
val res = arr match {
    case Array(head, tail...) => s"Head: $head, Tail size: ${tail.Size()}"
    case _ => "Empty"
}
```

### Rest Pattern Variants

- `_...` — match and discard remaining elements
- `name...` — match and capture remaining elements as a sequence

```gala
val list = ListOf("a", "b", "c", "d")

// Capture first two, bind rest
val res = list match {
    case List(first, second, rest...) => s"$first, $second, rest size: ${rest.Size()}"
    case _ => "Not enough elements"
}

// Check minimum length without capturing
val hasThree = list match {
    case List(_, _, _, _...) => "Has at least 3 elements"
    case _                   => "Less than 3 elements"
}
```

---

## Custom Extractors (Unapply)

Any struct with an `Unapply` method can be used as a pattern. Extractors come in two forms:

### Option-Returning Extractors (Value Extraction)

Return `Option[T]` — `Some` means the pattern matched, `None` means it did not:

```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None()

val number = 42
val description = number match {
    case Even(n) => s"$n is even"
    case _       => s"$number is odd"
}
```

### Boolean-Returning Extractors (Guard Patterns)

Return `bool` for simple yes/no matching without value extraction:

```gala
type Positive struct {}
func (p Positive) Unapply(i int) bool = i > 0
```

### Instance Extractors (Variable-Based)

In addition to type-based extractors (`Even{}.Unapply(x)`), GALA supports **instance extractors** — variables whose type defines `Unapply`. This is how `Regex` enables pattern matching:

```gala
import "martianoff/gala/regex"
import . "martianoff/gala/collection_immutable"

val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")

val result = "2024-01-15" match {
    case dateRegex(Array(year, month, day)) => s"$year-$month-$day"
    case _ => "not a date"
}
```

Here `dateRegex` is a `val` holding a `Regex` value. The transpiler detects that `Regex` has an `Unapply(string) Option[Array[string]]` method and generates `dateRegex.Unapply(input)`. The nested `Array(year, month, day)` pattern then destructures the capture groups using sequence matching.

Instance extractors work with any type that defines `Unapply` returning `Option[T]` or `bool`:

```gala
val emailRegex = regex.MustCompile("([\\w.]+)@([\\w.]+)")

val parts = "user@example.com" match {
    case emailRegex(Array(user, domain)) => s"User: $user, Domain: $domain"
    case _ => "not an email"
}
```

#### JSON Pattern Matching

The `json` package's `Codec[T]` works the same way — a codec value is an instance extractor that attempts to parse a JSON string into the target type:

```gala
import . "martianoff/gala/json"

struct Person(Name string, Age int)

val codec = Codec[Person](AsIs())

val result = jsonStr match {
    case codec(p) => s"Found: ${p.Name}"
    case _ => "invalid JSON"
}
```

---

## Boolean Exhaustive Match

Matching on a boolean with both `true` and `false` branches is exhaustive — no `case _` needed:

```gala
val desc = flag match {
    case true  => "enabled"
    case false => "disabled"
}
```

---

## Comparison: GALA match vs Go switch

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

<pre><code class="language-gala">val msg = shape match {
    case Circle(r)      => f"r=$r%.1f"
    case Rectangle(w,h) => f"$w%.0fx$h%.0f"
    case Point()        => "point"
}</code></pre>

</td>
<td>

<pre><code class="language-go">var msg string
switch shape._variant {
case Shape_Circle:
    msg = fmt.Sprintf("r=%.1f", shape.Radius.Get())
case Shape_Rectangle:
    msg = fmt.Sprintf("%fx%f", shape.Width.Get(), shape.Height.Get())
case Shape_Point:
    msg = "point"
}</code></pre>

</td>
</tr>
</table>

| Feature | GALA match | Go switch |
|---------|-----------|-----------|
| Expression (returns a value) | Yes | No |
| Struct destructuring | Yes | No |
| Exhaustive checking | Yes (sealed types, booleans) | No |
| Guard clauses | Yes (`if` after pattern) | No (separate `if` inside case) |
| Nested patterns | Yes | No |
| Custom extractors | Yes (Unapply) | No |
| Instance extractors | Yes (variable Unapply) | No |
| Sequence patterns | Yes (head, tail...) | No |
| Type matching | Yes (`case x: Type`) | Yes (`case Type:`) |

---

## Further Reading

- [Sealed Types](/features/sealed-types/) — defining closed type hierarchies
- [Error Handling](/features/error-handling/) — pattern matching on Option, Either, and Try
- [Immutability](/features/immutability/) — safe data by default
