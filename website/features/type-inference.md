---
layout: default
title: "Golang Type Inference — Lambda and Generic Type Inference for Go"
description: "GALA infers lambda parameter types, generic type params, and accumulator types from context. Write list.Map((x) => x * 2) without annotations — the transpiler resolves concrete Go types."
keywords: "golang type inference, go lambda type inference, golang generic type inference, go implicit typing, golang infer types, go generics inference, golang lambda, gala type inference"
permalink: /features/type-inference/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/type-inference/">Features</a> / Type Inference</p>

# Type Inference — Less Typing, Same Safety

GALA is statically typed, but you rarely need to write type annotations. The compiler infers variable types, lambda parameter types, generic type arguments, and accumulator types from context. The result is code that reads like a dynamic language but compiles with full type safety.

```gala
val nums = ArrayOf(1, 2, 3, 4, 5)
val doubled = nums.Map((x) => x * 2)
val sum = nums.FoldLeft(0, (acc, x) => acc + x)
```

In the snippet above, GALA infers:
- `nums` is `Array[int]` from the arguments to `ArrayOf`
- `x` in the Map lambda is `int` from the collection's element type
- `acc` in FoldLeft is `int` from the zero value `0`
- `x` in FoldLeft is `int` from the collection's element type

No type annotations needed. No `any` or `interface{}` in the generated Go code.

---

## Variable Type Inference

The type of `val` and `var` bindings is inferred from the right-hand side:

```gala
val x = 42                  // int
val name = "Alice"          // string
val pi = 3.14159            // float64
val active = true           // bool
val pair = Tuple(1, "two")  // Tuple[int, string]
val opt = Some(42)          // Option[int]
```

You can add an explicit type annotation when needed — for example, to assign to an interface type or to use a wider type:

```gala
val x float64 = 42          // float64 (not int)
val s Shaper = Circle(5.0)  // interface type
```

---

## Lambda Parameter Type Inference

When a lambda is passed to a method with known parameter types, the lambda's parameter types are inferred from the method signature. This is GALA's most impactful inference feature — it eliminates the most common type annotations in functional code.

### How It Works

```gala
// The compiler knows Array[int].Map takes func(int) U
// So (x) must be int
val doubled = ArrayOf(1, 2, 3).Map((x) => x * 2)

// The compiler knows Array[int].Filter takes func(int) bool
// So (x) must be int
val evens = ArrayOf(1, 2, 3).Filter((x) => x % 2 == 0)
```

### Supported Contexts

Lambda parameter inference works in these contexts:

**Generic receiver types** — Methods on `Array[T]`, `List[T]`, `Option[T]`, `HashMap[K,V]`, and other generic types. The receiver's type parameters are resolved and substituted into the method signature:

```gala
val opt = Some(42)
val doubled = opt.Map((x) => x * 2)         // x inferred as int
opt.ForEach((x) => { Println(x) })          // x inferred as int
val positive = opt.Filter((x) => x > 0)     // x inferred as int
```

**Non-generic wrapper types** — Methods on concrete types that take function parameters:

```gala
val s = S("hello")
val upper = s.Map((r) => r - 32)             // r inferred as rune
val hasVowel = s.Exists((r) => r == 'a')     // r inferred as rune
```

**Free function calls** — Lambda parameters are also inferred when passed to generic free functions:

```gala
val result = identity((x) => x * 2)
```

---

## Generic Method Type Parameter Inference

When you call a generic method, GALA infers the method's type parameters from the concrete argument types. You almost never need to write them explicitly:

```gala
// Map[U] — U is inferred as int from the lambda return type
val doubled = ArrayOf(1, 2, 3).Map((x) => x * 2)

// Instead of the explicit form:
// val doubled = ArrayOf(1, 2, 3).Map[int]((x int) => x * 2)
```

This works for all generic methods including `Map`, `FlatMap`, `Filter`, `FoldLeft`, `Zip`, `Collect`, and more.

---

## FoldLeft Accumulator Inference

The accumulator type parameter `U` in `FoldLeft[U](zero U, f func(U, T) U)` is inferred from the zero value argument:

```gala
val nums = ArrayOf(1, 2, 3)

// acc is int because the zero value is 0 (int)
val sum = nums.FoldLeft(0, (acc, x) => acc + x)

// acc is string because the zero value is "" (string)
val csv = nums.FoldLeft("", (acc, x) => acc + s"$x,")

// acc is float64 because the zero value is 0.0 (float64)
val avg = nums.FoldLeft(0.0, (acc, x) => acc + float64(x))
```

No explicit type annotation on the accumulator parameter, the zero value, or the generic type parameter.

---

## Generic Constructor Inference

When constructing generic types, type parameters are inferred from the arguments:

```gala
// Inferred: Some[int]
val x = Some(42)

// Inferred: Right[string, int]
val r = Right[string, int](42)

// Inferred: Tuple[int, string]
val t = Tuple(1, "hello")

// Inferred: ListOf creates List[int]
val list = ListOf(1, 2, 3)

// Inferred: HashMapOf creates HashMap[string, int]
val m = HashMapOf(("a", 1), ("b", 2))
```

Write `Some(42)`, not `Some[int](42)`. Write `ListOf(1, 2, 3)`, not `ListOf[int](1, 2, 3)`.

---

## Sealed Type Constructor Inference

Sealed type variant constructors infer their types from the arguments:

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val c = Circle(5.0)          // Shape, no type annotation needed
val r = Rectangle(3.0, 4.0)  // Shape
```

### Zero-field variants of a generic sealed type

A zero-field case of a *generic* sealed type — `None()` for `Option[T]`, or
any `case Empty()`-style variant — has no argument to infer its type parameter
from. GALA fills it in by **downward inference**: the type flows in from the
surrounding context, so you write `None()`, not `None[T]()`, whenever the
context pins the type.

```gala
// Return type pins the element type
func parseObject(v JsonValue) Option[Array[JField]] = v match {
    case JObj(fields) => Some(fields)
    case _            => None()        // inferred as Option[Array[JField]]
}

// A val annotation pins it
val empty Option[int] = None()         // inferred as Option[int]

// An if/else sibling pins it
func pick(b bool) Option[int] = if (b) Some(1) else None()
```

When **no** context pins the type — for example a bare `None()` inside a lambda
whose result type is unconstrained — the type genuinely cannot be determined,
and GALA asks you to annotate it explicitly (`None[int]()`) rather than guess.

---

## What Is NOT Inferred

GALA does not infer types in these contexts — you must provide explicit annotations:

**Top-level function parameters and return types:**

```gala
// Parameters MUST have type annotations
func add(a int, b int) int = a + b

// Return type MUST be specified
func greet(name string) string = s"Hello, $name"
```

**Interface implementations** — when a value needs to satisfy an interface, you may need to annotate the variable:

```gala
val s Shaper = Circle(5.0)  // explicit interface type
```

**Ambiguous literals** — when the compiler cannot determine which numeric type you mean:

```gala
val x float64 = 42  // 42 would default to int without annotation
```

---

## Best Practices: When to Annotate vs When to Omit

| Context | Recommendation |
|---------|----------------|
| `val x = 42` | Omit — inferred as int |
| `val x = Some(42)` | Omit — inferred as Option[int] |
| Lambda parameters in pipelines | Omit — inferred from method signature |
| FoldLeft accumulator | Omit — inferred from zero value |
| Generic method type params | Omit — inferred from arguments |
| Function parameters | Annotate — always required |
| Function return types | Annotate — always required |
| Interface variable assignment | Annotate — needed for interface dispatch |
| Wider numeric types | Annotate — `val x float64 = 42` |

The general rule: **omit types inside function bodies, annotate types at function boundaries**.

---

## Two-Layer Inference Architecture

Under the hood, GALA uses a two-layer inference system:

1. **Layer 1 (Pattern-based)** — Fast inference that handles 90%+ of cases: literals, scope lookups, function call return types, struct field types, and operator results.

2. **Layer 2 (Hindley-Milner)** — Full unification-based inference for complex cases: generic function instantiation, lambda parameter inference, and polymorphic type schemes.

The compiler tries Layer 1 first for speed. If it returns an unresolved type (containing type parameters like `T`), Layer 2 takes over with Algorithm W unification. This two-layer approach keeps compile times fast while handling complex generic code correctly.

---

## Further Reading

- [Functional Collections]({{ '/features/collections/' | relative_url }}) — see type inference in action with collection pipelines
- [Error Handling]({{ '/features/error-handling/' | relative_url }}) — type inference with Option, Either, and Try monads
- [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) — type inference in match expressions
