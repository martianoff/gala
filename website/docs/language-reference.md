---
layout: default
title: "GALA Language Reference - Complete Specification"
description: "Complete GALA language specification. Variables, functions, structs, sealed types, pattern matching, generics, lambdas, interfaces, control flow, and standard library types — the full reference for the Go alternative language."
keywords: "gala language reference, gala specification, gala syntax, gala language guide, go alternative language reference, gala documentation"
permalink: /docs/language-reference/
last_modified_at: 2026-10-09
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Language Reference</p>

{% raw %}

**Related pages:** [Why GALA?](/docs/why-gala/) | [Code Examples](/docs/examples/) | [Immutable Collections](/docs/immutable-collections/) | [Mutable Collections](/docs/mutable-collections/) | [Streams](/docs/streams/) | [Strings](/docs/strings/) | [Time Utilities](/docs/time-utils/) | [Dependency Management](/docs/dependency-management/)

# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

---

## Table of Contents

1. [Project Structure](#1-project-structure)
2. [Variable Declarations](#2-variable-declarations)
3. [Functions](#3-functions)
4. [Types and Structs](#4-types-and-structs)
5. [Interfaces](#5-interfaces)
6. [Control Flow](#6-control-flow)
7. [Functional Features](#7-functional-features)
8. [Generics](#8-generics)
9. [Standard Library Types](#9-standard-library-types)
10. [Literals and Type Conversions](#10-literals-and-type-conversions)
11. [Go Built-in Functions](#11-go-built-in-functions)
12. [Immutability Under the Hood](#12-immutability-under-the-hood)
13. [GALA Packages](#13-gala-packages)
14. [Embedding Files](#14-embedding-files)
15. [Testing](#15-testing)
16. [Best Practices](#16-best-practices)
17. [Dependency Management](#17-dependency-management)
18. [Further Reading](#18-further-reading)
19. [IDE Support](#19-ide-support)

> **Note:** This is the complete language specification. For the full, continuously updated source, see the [GALA.MD file on GitHub](https://github.com/martianoff/gala/blob/master/docs/GALA.MD). The content below is a faithful copy of that specification.

---

## 1. Project Structure {#1-project-structure}

GALA files use the `.gala` extension. Every file must start with a package declaration, followed by an empty line. All GALA files in the same directory must belong to the same package.

GALA supports Go-style imports, including aliases and dot imports. Import declarations must also be followed by an empty line.

```gala
package main

import (
    "fmt"
    m "math"
    . "net/http"
)
```

### Multi-File Packages

A GALA package can span multiple `.gala` files. Types, sealed types, and functions defined in one file are available in all other files of the same package. Each file must declare the same package name.

```
shapes/
  types.gala      # struct Point, sealed type Shape
  ops.gala        # func (p Point) String(), func Describe(s Shape)
```

In Bazel, list all source files in `srcs`:

```python
gala_library(
    name = "shapes",
    srcs = ["shapes/types.gala", "shapes/ops.gala"],
    importpath = "myapp/shapes",
)

gala_binary(
    name = "app",
    srcs = ["main.gala", "helpers.gala"],
)
```

## 2. Variable Declarations {#2-variable-declarations}

GALA distinguishes between immutable and mutable variables.

### Immutable (`val`)
Variables declared with `val` are immutable. They must be initialized at declaration time.

```gala
val x = 10
val a, b = 1, 2
// x = 20 // Compile error: cannot assign to immutable variable
```

### Mutable (`var`)
Variables declared with `var` are mutable and can be reassigned.

```gala
var y = 20
y = 30 // OK
```

### Discarding a value
A declaration whose only name is `_` binds nothing, so `val _ = expr` and
`var _ = expr`, with or without a type, and `_ := expr` are an error,
[GALA-E0060](/docs/errors/gala-e0060/). Write the
expression as a statement; if the value matters, bind it to a name and use it.
Inside a lambda with no result, a call that returns only an `error` cannot be a
bare statement, so wrap it: `FromError(file.Close())`.

<!-- doc-check: error GALA-E0060 -->
```gala
func main() {
    val _ = os.Remove("out.tmp")   // error: GALA-E0060
}
```

```gala
func main() {
    os.Remove("out.tmp")           // the error is dropped
}
```

`_` among several names (`val n, _ = strconv.Atoi(s)`, `val (_, b) = pair`)
is not affected.

### Short Variable Declaration
Inside functions, `:=` declares **immutable** variables.

```gala
func main() {
    z := 40
    // z = 50 // Compile error: cannot assign to immutable variable
}
```

## 3. Functions {#3-functions}

GALA supports both Go-style block functions and Scala-style expression functions.

### Block Functions
```gala
func add(a int, b int) int {
    return a + b
}
```

A block-bodied function with a result type returns its trailing expression, exactly as
a block lambda or a match arm does — the `return` is optional. The trailing value takes
its type from the result type, as a `return` value would, and a trailing `if`/`else`
carries the value in its branches:

```gala
func logged[U any](o Option[U]) Option[U] {
    Println("visited")
    o                                  // same as `return o`
}

func sign(n int) string {
    if (n < 0) { "negative" } else { "non-negative" }
}
```

Prefer the implicit trailing value. `return` stays valid, both at the end and for early
exits such as `if (n < 0) { return None() }`. The body must end in a value, a `return`, or
a diverging call such as `Panic(...)`. A
body that can finish without a value — a trailing `Println(...)`, an assignment, an `if`
with no `else` — is a compile error. A value computed only to be thrown away (a bare
name, literal or operator expression as a statement, including the trailing statement
of a function with no result type) is rejected as evaluated but not used.

### Expression Functions
```gala
func square(x int) int = x * x
func greet(name string) = Println(s"hello $name")
```

The result type is never inferred. Without one the function is void, like `func f() { <expr> }`: a call is made and its result discarded, and a `match` or `if` runs its branches as statements. A plain value (`func answer() = 42`) is rejected as evaluated but not used; write the result type to return it, `func answer() int = 42`. The same holds for each branch of that `match` or `if`, and for the branches of any `match` or if-expression used as a statement: a branch may make a call, assign or do nothing, but a plain value in one (`func pick(c bool) = if (c) 1 else 2`) is evaluated but not used.

### Local Functions
Named functions and methods are declared only at the top level of a file. A
function local to a body is a lambda bound to a `val`; a lambda states its
result type after the parameter list, exactly where a function does:

```gala
import . "martianoff/gala/collection_immutable"

func report(scores Array[int]) string {
    val clamp = (s int) int => if (s < 0) 0 else s
    return scores.Map((s) => s"${clamp(s)}").MkString(", ")
}
```

A recursive local helper is declared through `var` and then assigned, so the
lambda can refer to it:

```gala
var fact func(int) int
fact = (n int) => if (n <= 1) 1 else n * fact(n - 1)
```

A named `func` inside a body is rejected with
[GALA-E0052](/docs/errors/gala-e0052/), whose hint spells out the equivalent
lambda. A lambda takes no type parameters, so a generic helper stays at the top
level.

### Parameters
A function, method or lambda parameter is an immutable binding, like a `val`:
reassigning it — with `=`, a compound assignment such as `+=`, or `++`/`--` —
is a compile error, `cannot assign to immutable variable data`, whose hint is
``declare it `var data` to reassign it``. Mark a parameter `var` to make it
reassignable. Its address (`&data`) is a read-only `ConstPtr`, as for a `val`.
Writing `val` is allowed but redundant: `val label string` means
exactly the same as `label string`. A method's receiver is never reassignable
(see [Receivers](#receivers)).

```gala
func process(data string, val label string, var count int) {
    // data = "new"    // Error: cannot assign to immutable variable data
    // label = "other" // Error: the same — `val` is the default
    count = count + 1 // OK: a `var` parameter
}
```

Whatever its keyword, a parameter is a plain Go parameter of its declared type
in the generated code — `func process(data string, label string, count int)`
above — so a call passes its arguments as they are and Go code can call the
function directly. Receivers and lambda parameters are plain Go parameters
too.

### Named Arguments

Function calls support named arguments. Named arguments can appear in any order — the compiler reorders them to match the function signature.

```gala
func divide(dividend int, divisor int) int = dividend / divisor

divide(divisor = 4, dividend = 20)  // 5 — reordered to divide(20, 4)
divide(20, 4)                        // 5 — positional works too
```

Named arguments work with struct construction, `Copy()` method overrides, and regular function calls.

### Default Parameter Values

Parameters can have default values. When a function is called without providing a defaulted argument, the default expression is injected at the call site. Default parameters must come after required parameters.

```gala
func connect(host string, port int = 8080, tls bool = true) string =
    s"$host:$port tls=$tls"

// Positional: omit trailing defaults
Println(connect("localhost"))                    // localhost:8080 tls=true
Println(connect("localhost", 3000))              // localhost:3000 tls=true
Println(connect("localhost", 3000, false))       // localhost:3000 tls=false

// Named arguments + defaults: skip any defaulted parameter
Println(connect("localhost", tls = false))       // localhost:8080 tls=false
Println(connect(host = "localhost", port = 443)) // localhost:443 tls=true
```

Default expressions are evaluated at each call site (not once at definition time):

```gala
func log(msg string, ts time.Time = time.Now()) {
    Println(s"[$ts] $msg")
}
```

Expression functions also support defaults:

```gala
func greet(name string, greeting string = "Hello") string = s"$greeting, $name!"
```

A default is checked against the parameter's declared type exactly as an explicit
argument would be. For a function-typed parameter, that means a lambda default
takes its parameter and result types from the declaration, and `nil` is a valid
default:

```gala
func shout(s string, decorate func(string) string = (x) => x + "!") string = decorate(s)
func run(hook func() int = nil) int = if (hook == nil) 0 else hook()

struct Backend(
    Name         string,
    Origin       func() Tuple[int, int] = () => (0, 0),
    CursorReport func(int, int)         = (r, c) => {},
)
```

The compiler validates defaults at compile time:
- Default expression type must match the parameter type
- Parameters with defaults must be contiguous at the end of the parameter list

### Higher-Order Functions
Functions are first-class values:

```gala
func apply(x int, f func(int) int) int = f(x)

func main() {
    val double = (x int) => x * 2
    val result = apply(5, double) // result = 10
}
```

### By-Name Arguments (Thunk Sugar)
When a parameter's expected type is a **zero-arg** function type (`func() T` or
void `func()`), you can pass a plain expression where a lambda is expected — it
is automatically lifted into `() => expr` and evaluated lazily each time the
thunk is called. This is what lets `Try` and `Future` read like direct calls:

<!-- doc-check: fragment -->
```gala
func runTwice(body func() int) int = body() + body()

func main() {
    val a = runTwice(compute())        // sugar — lifted to () => compute()
    val b = runTwice(() => compute())  // explicit form; identical

    val f = Future(loadFromDB(id))     // same as Future(() => loadFromDB(id))
    val r = Try(parseConfig(path))     // same as Try(() => parseConfig(path))
}
```

The type parameter is inferred from the lifted expression's own type
(`Future(compute())` where `compute() int` yields `Future[int]`). The lift is
purely additive: an argument that is already a function value is passed through
untouched, and multi-argument function parameters still require an explicit lambda.

### Variadic Functions
```gala
func sum(numbers ...int) int {
    var total = 0
    var i = 0
    for ; i < numbers.Size() ; {
        total = total + numbers[i]
        i = i + 1
    }
    return total
}
```

### Methods
```gala
type Box[T any] struct { Value T }

func (b Box[T]) GetValue() T = b.Value
func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))
```

#### Receivers
A receiver can never be rebound. `p = ...`, a compound assignment and
`for _, p = range ...` are compile errors, `cannot assign to receiver p`
(`p++` is `cannot increment/decrement receiver p`), whose hint is ``a receiver cannot be rebound; copy it into a local `var` (`var c = p`) if you need a mutable copy``.
This holds for value and pointer receivers alike; there is no `var` receiver,
and `val` on a receiver is allowed but redundant. The address of a receiver
(`&p`) is a read-only `ConstPtr`, as for a parameter — for a pointer receiver,
a `ConstPtr` to the pointer.

Mutation *through* the receiver is unaffected: a method may write the
receiver's `var` fields and call its mutating methods. Through a pointer
receiver the change reaches the caller; through a value receiver it changes
the method's own copy.

```gala
struct Counter(var N int)

func (c *Counter) Bump() {
    c.N = c.N + 1      // OK: writes a `var` field through the receiver
}

func (c Counter) Clamped() Counter {
    // c = Counter(0)  // Error: cannot assign to receiver c
    var out = c        // a changing copy is a local `var`
    if (out.N < 0) {
        out = Counter(0)
    }
    out
}
```

## 4. Types and Structs {#4-types-and-structs}

### Shorthand Struct Declaration
```gala
struct Person(name string, age int, var score int)
```

### Block Struct Declaration
```gala
type Person struct {
    Name string    // Immutable
    age  int       // Immutable
    var Score int  // Mutable
}
```

### Struct Construction

<!-- doc-check: fragment -->
```gala
val p1 = Person{Name: "Alice", Age: 30}   // Named fields (Go-style)
val p2 = Person("Bob", 25)                 // Positional (Functional-style)
val p3 = Person(age = 20, name = "Charlie") // Named arguments
```

A named argument that names no field is [GALA-E0045](/docs/errors/gala-e0045/), for a shorthand or block-form struct, and for a Go struct whose fields the Go type info lists. A struct declared in a hand-written `.go` file of the package itself (`package main` included) is built with named arguments as a Go composite literal, like an imported Go struct: `Bag(Items = go_interop.SliceOf("a"), Score = (n) => n * 2)`.

Outside its own package, a struct is constructible by call syntax only when every field the call sets is exported. For the shorthand form that is every field, since an omitted one takes its default in the same literal, so `lib.Box(1)` for `struct Box(N int, seen bool = false)` is [GALA-E0043](/docs/errors/gala-e0043/). Call a constructor function the package exports, or start from the zero value `lib.Box{}`.

### Automatic Copy and Equal Methods

<!-- doc-check: fragment -->
```gala
val p1 = Person("Alice", 30)
val p2 = p1.Copy(age = 31) // p2 is Person("Alice", 31)
val same = p1.Equal(p2)    // false
```

A `Copy` override can be any expression, lambdas included. A lambda's parameter types come from the field's declared type, as with a named constructor argument: `calc.Copy(Op = (x) => x + 1)`.

### Type Aliases {#type-aliases}

`type X Y` declares an **alias**: a second name for an existing type, not a new type. The alias and its target are the same type and are interchangeable everywhere — a value of one *is* a value of the other, with no conversion and no separate identity.

<!-- doc-check: fragment -->
```gala
type MyString string
type Millis   int64
type Handler  func(Request) Response
type Coord    Point                    // Point is a struct in this package
```

Aliases transpile to Go type aliases (`type X = Y`), which is what makes them transparent: methods on the target are available through the alias, and an alias declared in one file is visible to every file in the same package.

An alias can take type parameters. `Conv[int, string]` below *is* `func(int) string`, so a lambda in its place is typed by it:

```gala
type Conv[A any, B any] func(A) B

struct Step[A any, B any](In A, Run Conv[A, B])

func main() {
    val step = Step(In = 3, Run = (x) => s"<$x>")   // x is int, the result string
    Println(step.Run(step.In))
}
```

A generic alias transpiles to a Go generic alias (`type Conv[A any, B any] = func(A) B`), which Go accepts from 1.24 on, so the `go.mod` files `gala build` generates declare `go 1.24`.

#### Generic aliases {#generic-aliases}

A generic alias names every instance of its target at once. It can take any number of parameters, with constraints (`comparable`, an interface), fix some of the target's arguments, or name another alias:

<!-- doc-check: fragment -->
```gala
type Result[T any] Either[AppError, T]            // fixes the error type
type Pairs[K comparable, V any] Array[Tuple[K, V]]
type StrMap[V any] HashMap[string, V]               // partially applied
type Again[T any] Result[T]                         // alias of an alias
```

`Result[int]` *is* `Either[AppError, int]`: constructors take their type arguments from it (`func parse(s string) Result[int] = if (s == "") Left(AppError(1)) else Right(s.Size())`), a match on it sees `Either`'s variants, and it has `Either`'s methods, codec encoding and shareability at a concurrency boundary. A generic alias takes no methods at all, whatever it names, and neither does an alias of one (`type IntResult Result[int]`) — Go has no methods through a generic alias ([GALA-E0048](/docs/errors/gala-e0048/)). A wrong number of type arguments or an argument that breaks a constraint is reported by the Go compiler, mapped to the GALA line.

#### What an alias can do {#what-an-alias-can-do}

| Use | Example | Notes |
|---|---|---|
| Annotate a type | `val s MyString = "hello"` | The alias stands wherever the target does |
| Convert | `Millis(v)`, `MyString(s)` | The ordinary [type conversion](#type-conversions) — `Millis(v)` *is* `int64(v)` |
| Construct, when the target is a struct | `Coord(1, 2)`; `Twin(1, 2)` for `type Twin[T any] Pair[T]` | Reaches the target's fields, so named and positional construction both work. A generic alias infers its type arguments from the fields and the expected type, as the struct does (`Twin(1, 2)` is a `Twin[int]`) |
| Carry a generic instantiation | `type IntPair Pair[int]` then `IntPair(1, 2)` | The alias names the instantiation; do not re-apply type arguments. It takes no methods — see below |
| Take methods, when the target is a **plain local type** | `func (c Coord) Sum() int = c.X + c.Y` | Legal because the receiver base type `Point` is declared in this package |

#### What an alias cannot do {#what-an-alias-cannot-do}

**It cannot take a method unless its target is a plain type declared in this package.** The receiver base type is the *target*, and Go accepts a method only on a plain, locally declared type — so a built-in, an imported type, an unnamed composite (slice, map, func), an instantiated type and any generic alias are all [GALA-E0048](/docs/errors/gala-e0048/):

<!-- doc-check: fragment -->
```gala
type Millis int64
func (d Millis) Value() int64 = int64(d)     // built-in target

type Dur time.Duration
func (d Dur) Ticks() int64 = 1               // imported target

type Handler func(Request) Response
func (h Handler) Name() string = "h"         // unnamed composite

type IntPair Pair[int]
func (p IntPair) First() int = p.A           // instantiated type

type Box[T any] Cell[T]
func (b Box[T]) Show() string = "box"         // generic alias, whatever it names
```

The alias chain is followed to its end, so `type A int64; type B A` makes a method on `B` illegal for the same reason. A pointer target is fine when it points at a local type — `type PP *Point` puts the method on `Point`.

Declare an [opaque type](#opaque-types) when you need methods of your own:

```gala
opaque type Millis int64

func (m Millis) Seconds() float64 = float64(m) / 1000.0
```

**It does not create a distinct type.** An alias cannot be matched, overloaded or type-switched apart from its target, and it does not make an illegal assignment illegal — `MyString` and `string` are one type. For a separate identity, declare an [opaque type](#opaque-types).

> **Note:** Type aliases are generally not recommended. Prefer using the original type directly — it keeps code clearer and avoids indirection. Type aliases are mainly useful for Go interop scenarios where you need to bridge between GALA and existing Go type names, or when mixing `.gala` and `.go` files in the same package (where GALA type aliases avoid dot-import conflicts with Go type alias declarations).

#### Alias or single-field struct {#alias-or-single-field-struct}

To give a value a name of its own, choose between an alias and a struct with one field. They differ in what the compiler checks and in what a codec writes:

| | `type UserID int64` | `struct UserID(Value int64)` |
|---|---|---|
| Distinct type | No — an `int64` is accepted where a `UserID` is expected | Yes — an `int64` is not a `UserID` |
| Methods of its own | No — [GALA-E0048](/docs/errors/gala-e0048/) | Yes |
| Arithmetic and ordering | Yes — `id + 1`, `a < b` | No — Go rejects `+` and `<` on a struct; use `.Value` |
| `HashMap` key | Yes, hashed as its target | Only with a `Hash() uint32` method ([`Hashable`](/docs/immutable-collections/#hashable-interface)); without one, `Put` panics with `HashMap: type main.UserID must implement std.Hashable interface` |
| JSON / YAML field, `SnakeCase()` | The bare value: `{"user_id":42}`, `user_id: 42` | An object: `{"user_id":{"value":42}}`, `user_id:` over a nested `value: 42` |

```gala
package main

import (
    "martianoff/gala/json"
    "martianoff/gala/yaml"
)

type UserID int64

struct AccountID(Value int64)

struct Login(UserID UserID, AccountID AccountID)

func main() {
    val login = Login(42, AccountID(7))
    Println(json.Codec[Login](json.SnakeCase()).Encode(login).Get())
    // {"user_id":42,"account_id":{"value":7}}
    Println(yaml.Codec[Login](yaml.SnakeCase()).Encode(login).Get())
    // user_id: 42
    // account_id:
    //   value: 7
}
```

Pick the alias to keep a plain value on the wire; pick the struct for a type a bare `int64` cannot pass for, or one with methods. The two encode differently, so switching breaks documents already written: `{"user_id":42}` read as the struct form is `Failure(json at pos 11: expected '{')`.

An [opaque type](#opaque-types) combines the two: a type a bare `int64` cannot pass for, with methods and operators, that is still written as the bare value.

### Opaque Types {#opaque-types}

`opaque type X Y` declares a **new, distinct type** whose values are represented exactly like values of `Y`, its *underlying type*. It transpiles to a Go defined type (`type X Y`), so it costs nothing at run time.

```gala
package main

opaque type UserID int64
opaque type Millis int64

func (m Millis) Seconds() float64 = float64(m) / 1000.0

func lookup(id UserID) string = s"user ${int64(id)}"

func main() {
    val id = UserID(42)              // construct: a conversion
    val raw = int64(id)              // unwrap: a conversion
    val wait = Millis(1500) + 500    // the underlying type's operators, on Millis
    Println(lookup(id), raw, wait > Millis(1000), wait.Seconds())
    // user 42 42 true 2
}
```

**Distinct.** `UserID`, `int64` and another `opaque type OrderID int64` are three types. A value never converts implicitly between an opaque type and its underlying type, or between two opaque types — at an argument, a `val` / `var` declaration or assignment, a return, or a constructor field — and GALA reports [GALA-E0064](/docs/errors/gala-e0064/) with the conversion to write:

<!-- doc-check: error GALA-E0064 -->
```gala
package main

opaque type UserID int64

func lookup(id UserID) string = s"user ${int64(id)}"

func main() {
    val raw int64 = 42
    Println(lookup(raw))      // error: write lookup(UserID(raw))
}
```

Untyped constants still mix, as in Go: `id == 42`, `lookup(7)`, `val zero Millis = 0` and `wait + 500` are all fine.

**Conversion is unrestricted.** `UserID(n)` and `int64(id)` are ordinary conversions, legal in every package, so an opaque type keeps IDs and units apart but does **not** hide its representation: any caller can build a `UserID` from any `int64`. A value that must be impossible to build without a check is a struct with a private field and a constructor function (see the comparison below). Converting one opaque type straight into another, `OrderID(userID)`, is [GALA-E0063](/docs/errors/gala-e0063/); when the change of kind is intended, go through the underlying type: `OrderID(int64(userID))`.

**Operators** come from the underlying type and work on the opaque type itself: `+`, `-`, `<`, `==` on two `Millis` give a `Millis` (or a `bool`). Whether `Millis * Millis` means anything is up to you.

**Methods.** An opaque type starts with no methods: those of its underlying type are **not** inherited — `opaque type Timeout time.Duration` has no `.Seconds()`. Declare methods in GALA, or in a hand-written `.go` file of the same package. A `String() string` method makes the type a `fmt.Stringer`, used by `Println` and string interpolation.

**Hash and Compare are generated**, so an opaque type works as a `HashMap` or `HashSet` key and in `TreeSet`, `TreeMap` and `Sorted()`: `Hash() uint32` for every opaque type, and `Compare(other T) int` (which makes it an `Ordered[T]`) for every one except those over `bool`. Each is skipped when the type already declares it, in GALA or in a `.go` file of the package. Equality is Go's `==`. Hover, completion and `gala doc` list the generated methods, marked *synthesized*.

```gala
package main

import . "martianoff/gala/collection_immutable"

opaque type UserID int64

func main() {
    val names = EmptyHashMap[UserID, string]().Put(UserID(2), "bo").Put(UserID(1), "al")
    Println(names.Get(UserID(1)), ArrayOf(UserID(3), UserID(1), UserID(2)).Sorted())
    // Some(al) Array(1, 2, 3)
}
```

**Underlying types.** An opaque type is declared over a scalar; anything else is [GALA-E0062](/docs/errors/gala-e0062/):

| Underlying type | Allowed |
|---|---|
| `bool`, `string`, every integer and floating-point kind, `rune`, `byte` | Yes |
| An alias that names one of those | Yes |
| A Go named scalar (`time.Duration`, `os.FileMode`) | Yes — its methods are not inherited |
| Another opaque type | No — declare it over the underlying type instead |
| A struct, a sealed type, a GALA collection | No — a distinct type over it loses all of its methods |
| A Go slice, map, pointer or channel; an interface; a function type | No |
| A bare type parameter (`opaque type Box[T any] T`) | No |

**Encoding.** A JSON or YAML codec writes an opaque type as its underlying value — as a struct field, inside an `Option`, `Array` or `List`, and as a `HashMap` key (over `string`) or value — including an opaque type declared in another package or instantiated with phantom type arguments, and as the root value (`json.Value[UserID]().Encode(UserID(42))` is `42`). `struct User(Id UserID)` is `{"id":42}`.

**Pattern matching.** `case UserID(n)` unwraps an opaque value and matches its
one sub-pattern against the underlying value: `n` binds an `int64`,
`UserID(0)` compares it with a literal, `UserID(_)` ignores it. Any other number
of sub-patterns is [GALA-E0065](/docs/errors/gala-e0065/). On an `any`, interface or type-parameter
subject the pattern first checks the value is a `UserID` — a plain `int64` is
not — and a phantom-typed one must spell its type arguments,
`case Id[User](n)`. Literal patterns, stable identifiers (`val Admin Role = 1`
then `case Admin`) and type patterns (`case u: UserID`) work as for any type. An opaque type is not sealed,
so a match on one ends in `case _`.

```gala
package main

opaque type UserID int64

func describe(id UserID) string = id match {
    case UserID(0) => "nobody"
    case UserID(n) if n < 0 => s"invalid $n"
    case UserID(n) => s"user $n"
    case _ => "unreachable"
}

func main() {
    Println(describe(UserID(0)), describe(UserID(7)))   // nobody user 7
}
```

**Phantom type parameters.** An opaque type may take type parameters that its
underlying type does not use, so one declaration gives each entity its own ID
type. `Id[User]` and `Id[Order]` are both an `int64` at run time, but neither
passes for the other ([GALA-E0064](/docs/errors/gala-e0064/)), and converting one
into the other directly is [GALA-E0063](/docs/errors/gala-e0063/):

```gala
package main

struct User(Name string)
struct Order(Total int)

opaque type Id[T any] int64

func (i Id[T]) Next() Id[T] = i + 1

func orderTotal(id Id[Order]) int = int(id) * 10

func main() {
    val ada = Id[User](1)
    Println(ada.Next(), orderTotal(Id[Order](7)), orderTotal(Id[Order](int64(ada))))
    // 2 70 10
}
```

A bare type parameter cannot be the underlying type itself:
`opaque type Box[T any] T` is [GALA-E0062](/docs/errors/gala-e0062/).

An opaque type is declared at the top level of a file; `opaque` is a keyword. Its zero value is the underlying zero value (`UserID(0)`); use `Option[UserID]` for "no ID".

#### Alias, opaque type or private-field struct {#alias-opaque-type-or-private-field-struct}

| | `type UserID int64` | `opaque type UserID int64` | `struct Email(v string)` |
|---|---|---|---|
| Distinct from the underlying type | No — it *is* `int64` | Yes ([GALA-E0064](/docs/errors/gala-e0064/)) | Yes |
| Methods of its own | No ([GALA-E0048](/docs/errors/gala-e0048/)) | Yes | Yes |
| Operators and ordering | Yes | Yes, on the opaque type | No |
| Who can build one | Anyone — it is an `int64` | Anyone: `UserID(n)` in every package | Only its own package; others call the constructor function it exports |
| Unwrapping | Nothing to unwrap | `int64(id)` | Only through a method its package exports |
| `HashMap` key, `Sorted` | Yes | Yes (generated `Hash` / `Compare`) | Only with `Hash` / `Compare` methods |
| JSON / YAML | The bare value | The bare value | An object, decodable only through `Validate` ([private fields](/docs/json/#structs-with-private-fields)) |
| Run-time cost | None | None | A struct, with an `Immutable` box per field |

Use an alias to give an existing type a second name (mostly for Go interop), an opaque type for IDs, units and other values that must not be mixed up, and a struct with a private field for a value that must be valid by construction — an email address, a non-empty name.

### Sealed Types (Algebraic Data Types)

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val c = Circle(3.14)
val desc = c match {
    case Circle(radius) => f"radius=$radius%.2f"
    case Rectangle(w, h) => f"$w%fx$h%f"
    case Point() => "point"
}
```

A variant is a constructor and a pattern, not a type: `Circle(3.14)` is a `Shape`. Naming a variant where a type is expected — `func radius(c Circle)`, `Array[Some[int]]`, `case c: Circle` — is [GALA-E0061](/docs/errors/gala-e0061/); take the sealed type and match on the variant.

Generic sealed types:
```gala
sealed type Result[T any] {
    case Ok(Value T)
    case Err(Error error)
}
```

## 5. Interfaces {#5-interfaces}

```gala
type Shaper interface {
    Area() float64
}

struct Rect(width float64, height float64)
func (r Rect) Area() float64 = r.width * r.height
```

## 6. Control Flow {#6-control-flow}

### If Statement and Expression

<!-- doc-check: fragment -->
```gala
val status = if (score > 50) "pass" else "fail"
```

### Match Expression
A default case is required unless the arms cover every value: all variants of a sealed type, both `true` and `false`, or an unguarded arm that matches anything — `case _`, a plain binding (`case n`), or a tuple pattern made only of wildcards, bindings and nested such tuples (`case (_, _, err)`).

<!-- doc-check: fragment -->
```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => s"Value is $n"
    case _ => "other"
}

// Boolean exhaustive match
val desc = flag match {
    case true  => "enabled"
    case false => "disabled"
}
```

A `match` used as a statement (its value discarded) is side-effect dispatch: its arms need not share a type, and each arm is a statement. An arm may make a call, assign or do nothing, but an arm that is, or in braces ends in, a plain value (a literal, name, operator expression or lambda) is rejected as evaluated but not used.

#### Stable Identifiers (Constants in Patterns)
A capitalized identifier in a `case` pattern that names a value in scope (a local or package `val`/`var`, a parameter, a binding of an enclosing arm, or a `const`/`var` from a hand-written `.go` file of the same package) compares with `==` instead of binding. A qualified name such as `math.MaxInt8` always compares. Lowercase identifiers never compare with a value: they bind (unless they name a variant of the matched sealed type or a zero-field extractor), so compare against a lowercase value with a guard. A name cannot appear twice in one pattern (`case (X, X)` is rejected), and a zero-field variant or extractor of the same name takes precedence.
```gala
type Environment string

val Development Environment = "development"

func mode(env Environment, limit Environment) string = env match {
    case Development     => "dev"       // env == Development
    case x if x == limit => "limit"     // lowercase value: use a guard
    case _               => "other"
}
```

#### Alternative Patterns
`p1 | p2 | …` matches when any alternative matches, at the top of a `case` or nested inside an extractor or tuple; a sealed match is exhaustive when its alternatives cover every variant. Alternatives bind no names: `case Some(n) | None()` is rejected ([GALA-E0071](/docs/errors/gala-e0071/)), so use `_` or one case per alternative. In a pattern `|` is always an alternative, also inside parentheses; match a bitwise OR with a guard (`case n if n == (FlagA | FlagB)`) or a named `val`. Parenthesize an alternative that uses `+`, `-` or `^`: `case (1 + 1) | 3`.
```gala
package main

func mode(m string) string = m match {
    case "debug" | "development" => "dev"
    case "prod" | "production"   => "prod"
    case _                       => "unknown"
}

func size(o Option[int]) string = o match {
    case Some(1 | 2 | 3)  => "small"
    case Some(_) | None() => "other"
}

func main() {
    Println(mode("debug"), size(Some(2)))
}
```

#### Type-Based Pattern Matching

<!-- doc-check: fragment -->
```gala
val res = x match {
    case s: string => s"Found string: $s"
    case i: int    => s"Found int: $i"
    case _         => "Unknown type"
}
```

A wildcard type argument, `case w: Wrap[_]`, matches any instantiation of a generic struct, at the top level or nested in another pattern (`case Failure(e: Tagged[_])`, `case Some(w: Wrap[_])`, a tuple element, a struct field, a sequence element). The binding has the type of the value being matched: its fields can be read when that type is an instantiation, and an interface's methods when it is an interface such as `error`. A value typed `any` is the exception: `Wrap[_]` asserts to `Wrap[any]` there, and matches only a `Wrap[any]`.

#### Struct Patterns on Interface Values
A struct pattern also matches a value whose static type is an interface — `any`, `error`, an interface the program declares, a Go interface — or a type parameter. The arm matches when the value holds that struct, and binds its fields:

```gala
struct NotFound(Key string)

func (e NotFound) Error() string = s"not found: ${e.Key}"

func describe(err error) string = err match {
    case NotFound(k) => s"missing $k"
    case _           => err.Error()
}
```

A struct the interface can never hold — one missing a method of the interface, or declaring it with a pointer receiver — is a compile error rather than an arm that never matches. A generic struct names its type arguments, `case Box[int](v)`, since Go can only check for one instantiation.

#### Extractors and Unapply
```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

val parity = 42 match {
    case Even(n) => s"$n is even"
    case _       => "odd"
}
```

#### Pattern Matching Filters (Guards)

<!-- doc-check: fragment -->
```gala
val res = x match {
    case i: int if i > 100 => "Large integer"
    case Person(name, age) if age < 18 => s"$name is a minor"
    case _ => "Other"
}
```

#### Sequence Pattern Matching
```gala
val arr = ArrayOf(1, 2, 3, 4, 5)
val res = arr match {
    case Array(head, tail...) => s"Head: $head, Tail size: ${tail.Size()}"
    case _ => "Empty"
}
```

### Monadic Binding (`bind` / `also`)

Inside a function whose result type is a monad `M`, `bind name = expr` unwraps `M` and binds the success value; the block lowers to a `FlatMap` chain, so every bound name stays in scope for later statements. `also` marks an *independent* clause: a `bind`+`also` group lowers through `Zip{N}` when the monad provides it — `Validated` (in the `validation` package) accumulates all errors, `Future` runs the clauses concurrently — and falls back to the sequential `FlatMap` chain for fail-fast monads (`Try`/`Option`/`Either`).

<!-- doc-check: fragment -->
```gala
// bind: each value stays in scope; the first Failure short-circuits.
func processOrder(id int) Try[Receipt] {
    bind o = fetchOrder(id)
    bind valid = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))
}

// also over Validated: report ALL invalid fields, not just the first.
func makePerson(name string, email string, age int) Validated[string, Person] {
    bind n = vName(name)
    also e = vEmail(email)
    also a = vAge(age)
    Valid(Person(n, e, a))
}
```

A Go call that returns `(T, error)` is already a `Try[T]` (see [Go functions that return several results](/docs/language-reference/#go-functions-that-return-several-results)), so `bind n = strconv.Atoi(text)` works directly inside a `Try` block.

Bound names are immutable `val`s. `bind`/`also` work over any user-defined monad that defines `FlatMap[U](f func(T) M[U]) M[U]`; a type without `FlatMap` is rejected with a clear compiler error. See [`bind` / `also` notation](https://github.com/martianoff/gala/blob/master/docs/BIND_NOTATION.MD) for the full specification and the user-monad extension guide.

### For Statement

<!-- doc-check: fragment -->
```gala
for i := 0; i < 10; i++ {
    Println(i)
}

for count < 5 {
    count++
}

for _, v := range items {
    Println(v)
}
```

### Break and Continue

`break` leaves the innermost enclosing `for` loop and `continue` advances it, as in Go. Both also work in an arm of a `match` used as a statement inside the loop:

```gala
for i := 0; i < 6; i++ {
    i match {
        case 1 => continue      // skips the rest of this iteration
        case 4 => { break }     // leaves the loop
        case _ => Println(i)
    }
}
// Prints 0, 2, 3
```

A `return` in such an arm likewise returns from the enclosing function.

So do `break`, `continue` and `return` in an arm of a `match`, or a branch of an if-expression, that a local `val` or `var` is initialized with — one name, or a tuple destructuring `val (a, b) = ...` — or a variable is assigned (`val x = i match { case 2 => break ... }`): the other arms give `x` its value.

They must reach a loop written around them in the same function. Outside any loop, inside a lambda (a separate function, even when written in a loop), inside an arm of a `match` or a branch of an if-expression whose value is used otherwise (`Println(i match { case 2 => break ... })`), or used as a value, `break` and `continue` are an error: [GALA-E0059](/docs/errors/gala-e0059/). A `return` in such a value is [GALA-E0069](/docs/errors/gala-e0069/).

## 7. Functional Features {#7-functional-features}

### Lambda Expressions

<!-- doc-check: fragment -->
```gala
val f = (x int) => x * x

// Inferred parameter types
val doubled = opt.Map((x) => x * 2)

// Inferred from a val of function type
val apply = (h func(string) string) => h("x")
val shouted = apply((s) => s + "!")

// Void closures
opt.ForEach((x) => { Println(x) })
```

A lambda parameter is immutable unless it is declared `var`, the same rule as
for a function parameter (see [Parameters](#parameters)): reassigning it is
`cannot assign to immutable variable x`, and `&x` is a read-only `ConstPtr`.

```gala
val clamp = (var n int) int => {
    if (n < 0) {
        n = 0          // OK: a `var` lambda parameter
    }
    n
}
val total = Some(5).Map((var n) => {
    n += 10            // `var` works on an inferred parameter too
    n
})
```

### Partial Function Literals

<!-- doc-check: fragment -->
```gala
val pf = { case 1 => "one" case 2 => "two" }
val r1 = pf(1)  // Some("one")
val r2 = pf(5)  // None[string]

// Use with Collect
val evenDoubled = numbers.Collect({ case n if n % 2 == 0 => n * 2 })
```

## 8. Generics {#8-generics}

```gala
func identity[T any](x T) T = x

type Box[T any] struct { Value T }
```

A call that writes only its leading type arguments infers the rest from the arguments — for a generic function, a generic struct constructor and a companion `Apply` alike: `Pair[int64](1, "one")` is a `Pair[int64, string]`. A generic struct construction takes the type parameters its arguments leave undetermined from its expected type — the result type it is returned as, an annotated `val`, a parameter, a field — when that names the same struct, directly or through an alias: with `struct Tag[T any](Name string)`, `func label() Tag[int] = Tag("x")` is a `Tag[int]`. Any type parameter still undetermined is an error naming it.

## 9. Standard Library Types {#9-standard-library-types}

### Option Monad
```gala
val x = Some(10)
val y = None[int]()

val msg = x match {
    case Some(v) => s"Got: $v"
    case None()  => "Empty"
}

val result = x.Map((i) => i * 2)
val value = x.GetOrElse(0)
```

### Tuple
```gala
val t = (1, "hello")
val (a, b) = t         // a = 1, b = "hello"

var (lo, hi) = (3, 9)  // var destructures into mutable variables
lo = lo * 2            // lo = 6
```

A destructuring declaration takes each name's type from the tuple, so it has exactly one initializer and no type annotation; anything else is [GALA-E0056](/docs/errors/gala-e0056/).

GALA functions return one value; to return several, return a Tuple and destructure it at the call site (`val (q, r) = divmod(17, 5)`).

#### Go functions that return several results {#go-functions-that-return-several-results}

Many Go functions return more than one result — most often a value and an
`error` that says whether the call worked. GALA expressions are always **one
value**, so a call like that is presented as one GALA value, automatically,
wherever it is used as a value:

| The Go function returns | A call of it is a | Example | Its GALA value |
|---|---|---|---|
| a value and an error `(T, error)` | `Try[T]` | `os.ReadFile(path)` | `Try[[]byte]` |
| two or more values and an error | `Try` of a Tuple | `net.SplitHostPort(addr)` | `Try[Tuple[string, string]]` |
| two values `(A, B)` | `Tuple[A, B]` | `math.Modf(x)` | `Tuple[float64, float64]` |
| three to ten values | `Tuple3` … `Tuple10` | `strings.Cut(s, "=")` | `Tuple3[string, string, bool]` |
| only an `error` | the `error` itself | `os.Remove(path)` | `error` (nil when it worked) |

A `Try` is `Success(value)` when the call worked and `Failure(err)` when it
returned an error, so the usual `Try` tools apply directly:

```gala
import (
    "os"
    "strconv"
    "strings"
)

val port = strconv.Atoi("8080") match {                  // Try[int]
    case Success(n) => n
    case Failure(_) => 80
}
val size = os.ReadFile("app.conf").Map((data) => data.Size()).GetOrElse(0)
val (key, value, found) = strings.Cut("port=8080", "=") // Tuple3 destructuring
```

This holds in every position that takes one value: a `val`, a match subject,
a function or method argument, a lambda or function result, an if or match
branch, a struct field, a receiver (`strconv.Atoi(s).Map(...)`). Passed to a
Go function such as `fmt.Println`, the GALA value is what is passed:
`fmt.Println(strconv.Atoi("7"))` prints `Success(7)`.

A Go function that returns only an `error` gives you that error as a value;
wrap the call in `Try(...)` to treat a non-nil error as a failure:
`Try(os.Remove(path))` is a `Try[Void]`. `FromError(os.Remove(path))` means the
same.

`Try(goCall())` is the call's own Try — the conversion is not applied twice —
so `Try(strconv.Atoi(s))`, `Try(() => strconv.Atoi(s))` and `strconv.Atoi(s)`
are all a `Try[int]`. Going through `Try(...)` also turns a panic inside the
call into a `Failure`. Any other by-name parameter takes the call's value like
an ordinary argument: `Future(os.ReadFile(path))` is a `Future[Try[[]byte]]`
that completes with the `Try`.

**Taking the results one by one.** A binding of several names (no
parentheses) still receives Go's results as they are, which is the way to hand
them straight back to Go code:

```gala
import (
    "os"
    "strconv"
)

val data, err = os.ReadFile("app.conf")  // data []byte, err error
var n, parseErr = strconv.Atoi("42")     // var: raw values, reassignable
```

The sole argument of a Go function whose parameters take the results one for
one is passed the same way, as Go does: `template.Must(tmpl.Parse(text))`.

**Using the Try where the plain value is expected** is a compile-time error,
[GALA-E0049](/docs/errors/gala-e0049/), which names the call and the ways to the
value:

```
error[GALA-E0049]: `data` holds the result of `os.ReadFile(...)`, which can fail, so it is a `Try[[]byte]`; `[]byte` is expected here
  = hint: take the value with `.Get()` (panics on failure), `.GetOrElse(default)`, or `match { case Success(v) => ... case Failure(e) => ... }`; or bind the results Go-style: `val v, err = os.ReadFile(...)`
```

A statement whose value is not used — `sb.WriteString("x")`, `fmt.Fprintf(w, ...)`
on its own line — calls the Go function as it is. So does a branch of an
if-expression, or an arm of a match, whose value is not used:
`if (verbose) fmt.Println(msg) else log.Print(msg)` on its own line is an if
statement.

#### Returning several results to Go {#returning-several-results-to-go}

The other direction: a function that **Go** calls with several results. A
function or method may declare Go's result list, two or more types in
parentheses, in place of its result type. The body computes the results' one
GALA value by the same table — a `Try[T]` for `(T, error)`, a `Tuple[A, B]` for
`(A, B)`, a `Try[Tuple[A, B]]` for `(A, B, error)` — and Go receives the results
(a `Failure(err)` is the zero value and `err`). This is how a GALA type
implements a Go interface whose methods return several results, such as
`io.Writer` or `json.Marshaler`:

```gala
import (
    "encoding/json"
    "errors"
    "fmt"
    . "martianoff/gala/go_interop"
)

struct LineCounter(var Lines int)

// io.Writer: Write(p []byte) (n int, err error)
func (c *LineCounter) Write(p []byte) (int, error) {
    c.Lines = c.Lines + 1
    Success(p.Size())
}

struct Celsius(Degrees int)

// json.Marshaler: MarshalJSON() ([]byte, error)
func (c Celsius) MarshalJSON() ([]byte, error) =
    if (c.Degrees < -273) Failure(errors.New("below absolute zero"))
    else Success(ToBytes(s"\"${c.Degrees}C\""))

func divmod(a int, b int) (int, int) = (a / b, a % b)

var counter = LineCounter(0)
fmt.Fprintf(&counter, "hello %s\n", "go")    // calls Write
val data = json.Marshal(Celsius(21))          // calls MarshalJSON
```

In GALA, a call of such a function is one value, exactly like a call of a Go
function with those results: `divmod(17, 5)` is a `Tuple[int, int]`,
`counter.Write(b)` a `Try[int]`, and `val n, err = counter.Write(b)` takes the
results one by one. A GALA interface may declare such a method, and a function
type may have such results: `func() (int, error)`. A call of any value of such
a type — a `val`, a struct field, the result of another call — is one value
too.

**A lambda** passed where a function with several results is expected — a Go
callback such as `func() (T, error)`, or a parameter of that function type —
works the same way: its value is spread over the results, and a body that is a
Go call with those results returns them as they are. So does a placeholder
lambda (`strconv.Atoi(_)`). With no parameters, the lambda can be written as
its bare body: `sync.OnceValues(strconv.Atoi(s))`. A
generic Go function takes its type arguments from the lambda's results:

```gala
import (
    "strconv"
    "sync"
)

val port = sync.OnceValues(() => strconv.Atoi("8080"))  // func() (int, error)
val p, err = port()                                     // 8080, nil
val next = port().Map((n) => n + 1)                     // Success(8081)
```

### Either
```gala
val e = Right[int, string]("success")
val msg = e match {
    case Left(code) => s"Error code: $code"
    case Right(s)   => "Result: " + s
}
```

### Try Monad

<!-- doc-check: fragment -->
```gala
val result = Try(riskyDivide(10, 0))  // by-name sugar: runs () => riskyDivide(...) lazily
val parsed = Try(strconv.Atoi("42"))  // Success(42) — the same Try[int] as strconv.Atoi("42") itself
val dir = Try(os.TempDir)             // function reference sugar (bare zero-arg call)

val msg = result match {
    case Success(n) => s"Got: $n"
    case Failure(e) => s"Error: ${e.Error()}"
}
```

#### Panic Stack Traces {#panic-stack-traces}

When `Try` turns a panic into a `Failure`, it also keeps the stack trace of the
panic, taken while the panicking frames are still on the stack. `PanicStack(err)`
reads it back: `Some(stack)` for the error of a `Failure` made from a panic (or
an error that wraps one), `None` for any other error, such as `FromError(err)`.
The frames point at `.gala` source lines, so a program that uses `Try` as a
safety boundary can log where the panic happened:

<!-- doc-check: fragment -->
```gala
Try(handle(request)).OnFailure((err) => {
    Println(s"request failed: $err")
    PanicStack(err).ForEach((stack) => Println(stack))
})
```

The stack travels with the error without changing what it says: `err.Error()`
and printing are the panic's own message, patterns (`case e: NotFound`,
`case NotFound(key)`) and `errors.Is` / `errors.As` see the panic's own error,
and two `Failure`s of the same panic value are `Equal`. The `Failure` holds a
wrapper of that error, so test it with `errors.Is(err, io.EOF)` or a pattern
rather than `==`, and with `errors.As` rather than a type switch in Go code.
The stack is captured only when a panic is recovered; a `Try` that succeeds
costs nothing extra.

#### Try with Go Functions {#try-with-go-functions}

A Go function that returns `(T, error)` already gives a `Try[T]` when its call
is used as a value (see [Go functions that return several
results](/docs/language-reference/#go-functions-that-return-several-results)): a non-nil error is the
`Failure`.

```gala
import "strconv"

val result = strconv.Atoi("42") match {
    case Success(n) => s"Parsed: $n"
    case Failure(err) => s"Error: ${err.Error()}"
}
// result: "Parsed: 42"
```

A Go function returning two or more values and an error gives a `Try` of a
Tuple, which a tuple pattern takes apart:

```gala
import "net"

// net.SplitHostPort returns (string, string, error): a Try[Tuple[string, string]]
val result = net.SplitHostPort("localhost:8080") match {
    case Success((host, port)) => s"host=$host port=$port"
    case Failure(err) => s"Error: ${err.Error()}"
}
// result: "host=localhost port=8080"
```

Writing `Try(...)` around such a call gives the same Try — never a Try inside
a Try — and additionally turns a panic inside the call into a `Failure`. It is
also how a Go function returning only an `error` becomes a `Try[Void]`:

| Go Return Signature | The call | `Try(call)` |
|---------------------|----------|-------------|
| `(T, error)` | `Try[T]` | `Try[T]` |
| `(A, B, error)` | `Try[Tuple[A, B]]` | `Try[Tuple[A, B]]` |
| `(A, B, C, error)` | `Try[Tuple3[A, B, C]]` | `Try[Tuple3[A, B, C]]` |
| `error` | `error` | `Try[Void]` |

### Future Monad

<!-- doc-check: fragment -->
```gala
import . "martianoff/gala/concurrent"

val async = Future[int](expensiveComputation())  // by-name: runs () => expensiveComputation() async
val result = async.Await()           // Returns Try[int]
val doubled = async.Map((v) => v * 2)
```

### Slices and Maps (Go Interop)
**Prefer GALA collections** over Go slices for most use cases. See [Immutable Collections](/docs/immutable-collections/).

```gala
import . "martianoff/gala/collection_immutable"
import . "martianoff/gala/go_interop"

// PREFERRED: GALA collections
val nums = ArrayOf(1, 2, 3, 4, 5)
val doubled = nums.Map((x) => x * 2)

// GO INTEROP: When you need []T (SliceOf lives in go_interop)
val goSlice = SliceOf(1, 2, 3)
```

## 10. Literals and Type Conversions {#10-literals-and-type-conversions}

### String Interpolation

<!-- doc-check: fragment -->
```gala
val name = "Alice"
val age = 30

Println(s"Hello $name!")                    // Hello Alice!
Println(s"$name is $age years old")         // Alice is 30 years old
Println(s"Next year: ${age + 1}")           // Next year: 31
Println(s"Price: $$99")                     // Price: $99

// Explicit format specs
Println(f"$count%04d items")                // 0007 items
Println(f"Total: $$$price%.2f")             // Total: $19.99
```

### Rune Literals
```gala
val asterisk = '*'
val space = ' '
val newline = '\n'
```

### Type Conversions
```gala
val n = int64(42)
val f = float64(10)
val r = rune(65)            // 'A'
val s = string(r)           // "A"
```

## 11. Go Built-in Functions {#11-go-built-in-functions}

Bare Go builtins are **not** part of GALA's surface: calling one is a transpile error ([GALA-E0035](/docs/errors/gala-e0035/)). Each has a GALA-native form or a sanctioned interop wrapper:

| Forbidden builtin | Use instead |
|---|---|
| `len(x)` | `x.Size()` (logical size; **characters** for strings) or `x.ByteSize()` (string bytes) |
| `cap(x)` | `go_interop.SliceCap(x)` |
| `new(T)` | `go_interop.New[T]()` (pointer), or a zero value / `Option[T]` |
| `append(s, v)` | `go_interop.SliceAppend(s, v)` / `SliceAppendAll(s, more)`, or an `Array`/`List` |
| `make([]T, n)` | `go_interop.SliceWithSize` / `SliceWithCapacity`; `MapEmpty` for maps |
| `delete(m, k)` | `go_interop.MapDelete(m, k)`, or `HashMap.Remove(k)` |
| `close(ch)` | `go_interop.CloseChan(ch)` (or `CloseSignal` for a signal channel) |
| `panic(v)` | `go_builtins.Panic(v)` — but prefer `Option` / `Try` / `Either` |
| `recover()` | not available — `Try` captures panics |

### Names That Are Go Keywords

GALA reserves some of Go's keywords itself (`func`, `type`, `struct`, `interface`, `map`, `if`, `else`, `for`, `range`, `return`, `case`, `var`, `import`, `package`). The others — `break`, `chan`, `const`, `continue`, `default`, `defer`, `fallthrough`, `go`, `goto`, `select`, `switch` — are still **reserved as names**, because every name a GALA program declares is a name in the Go it compiles to. Using one as a name is a transpile error ([GALA-E0055](/docs/errors/gala-e0055/)) in every position: a `val`, `var` or `:=` binding, a parameter or lambda parameter, a pattern binding, a struct field, a function or method, a type or type parameter, the package name, and an import alias. The error points at the declaration.

<!-- doc-check: error GALA-E0055 -->
```gala
val default = 8080          // GALA-E0055: "default" is a Go keyword
struct Job(select bool)     // GALA-E0055
```

Pick another name, such as `defaultPort` or `selected`. GALA does not rename the identifier for you: a renamed struct field or exported function would be visible to Go code, JSON field names and reflection under a name you never wrote. A bare `break` or `continue` statement in a `for` loop is loop control and is not affected; a bare `defer`, `go`, `goto`, `fallthrough`, `select` or `chan` statement is [GALA-E0036](/docs/errors/gala-e0036/).

Go's *predeclared* identifiers — `int`, `string`, `error`, `len`, `min`, `max` and so on — are not keywords. A local binding may shadow one, exactly as in Go.

## 12. Immutability Under the Hood {#12-immutability-under-the-hood}

### Package-Level Bindings
A package-level `val` is a `std.Immutable[T]` in the generated Go but reads as a plain `T` everywhere — its own file, sibling files, and other packages (qualified, aliased, or dot-imported). A package-level `var` stays a plain, reassignable variable.

<!-- doc-check: fragment -->
```gala
// package colors
val Green = NamedColor(2)
var Hits = 0

// package main
Println(colors.ToSgr(colors.Green))  // Green reads as colors.Color
colors.Hits = colors.Hits + 1        // OK: a var
// colors.Green = NamedColor(3)      // ERROR: cannot assign to immutable variable colors.Green
```

### Go Encoders and `fmt`
Go's reflection-based encoders and `fmt` see an immutable value as the value it holds, not the `std.Immutable[T]` wrapper: `encoding/json` and YAML libraries using the `MarshalYAML`/`UnmarshalYAML` convention (`gopkg.in/yaml.v2`, `v3`) encode and decode it as a `T`, and `%v` prints the value. A struct tag on an immutable field applies to the value. To `encoding/json` the field is still a struct, which `omitempty` never leaves out; use `omitzero` (YAML's `omitempty` works). A `json.Decoder`'s `DisallowUnknownFields` and `UseNumber` do not reach inside the field. Encoders and `fmt` only look inside exported fields: a lower-case field is skipped by encoders and printed by `%v` as its wrapper.

### Struct Fields Declared `Immutable[T]`
A shorthand struct field declared `Immutable[T]` is the same field as one declared `T`: it takes a `T` (positionally, by name, as a `Copy` override or as its default), wrapped once, and reads as a `T`. A `var` field, an explicit `val` field or a block-form field declared `Immutable[T]` holds the `Immutable[T]` it names.

```gala
struct Counter(Label string, Hits Immutable[int64] = 1)

val c = Counter(Hits = 3, Label = "named")
Println(c.Hits + 1)              // 4: Hits reads as an int64
```

### Pointer Types and Immutability
```gala
var data = 42
val ptr1 = &data     // immutable pointer binding
*ptr1 = 100          // OK: can modify data through pointer
// ptr1 = &other     // ERROR: cannot reassign immutable pointer
```

### ConstPtr - Read-Only Pointers
```gala
val data = 42
val ptr = &data  // ptr is ConstPtr[int]
val value = *ptr // OK: read
// *ptr = 100    // ERROR: cannot write through ConstPtr
```

A `ConstPtr` implements no interface of the value's type. Passing `&c` of a `val` where an interface such as `Notifier` is expected, and only `*Counter` implements it, is [GALA-E0070](/docs/errors/gala-e0070/); declare the value `var` to pass a `*Counter`.

### Pointer-Receiver Methods on a `val`
A method with a pointer receiver (a GALA `func (c *Counter) Bump()`, or Go's `url.URL.String`) can be called on a `val`, a field reached through one, a call result or a literal. Go only calls such a method on an addressable value, so it runs on a fresh copy and whatever it assigns to the receiver's own fields is lost. A `var`, a function parameter and a pattern binding are addressable, so there the method changes the variable itself.

```gala
struct Counter(var n int)

func (c *Counter) Bump() int {
    c.n = c.n + 1
    return c.n
}

func main() {
    val c = Counter(n = 1)
    Println(c.Bump())  // 2 — bumped a copy
    Println(c.n)       // 1 — the val is unchanged

    var m = Counter(n = 10)
    m.Bump()
    Println(m.n)       // 11
}
```

The copy is shallow: writes through a map, slice or pointer the value holds reach data the `val` shares. A value that must not be copied is rejected instead with [GALA-E0053](/docs/errors/gala-e0053/): any `sync` or `sync/atomic` type, a type whose pointer has `Lock()`/`Unlock()`, a `noCopy` marker, a struct holding one of them, `strings.Builder` and `bytes.Buffer`. Hold such a value in a `var` or behind a pointer (`&sync.Mutex{}`).

## 13. GALA Packages {#13-gala-packages}

GALA supports Go-style imports with aliases and dot imports.

```gala
import m "math"
import . "martianoff/gala/examples/mathlib"

func main() {
    val res = m.Sqrt(16.0)
    val sum = Add(10, 20) // from mathlib via dot import
}
```

A few std names are reserved and cannot be redeclared, because the language gives them built-in meaning: `Option`, `Either`, `Try`, `Immutable`, `Tuple`…`Tuple10`, `Traversable`, `Iterable`, `Sendable`, `EmbeddedFS`, the companions `Some`, `None`, `Left`, `Right`, `Success`, `Failure`, and helpers such as `Copy` and `Equal`. A `type`, `struct` or top-level function under one of them is an error, and a sealed variant named like a companion draws a warning.

A package-level declaration whose name a dot-imported package also exports is rejected with [GALA-E0066](/docs/errors/gala-e0066/), because Go forbids it; import that package under a name, or rename the declaration.

### Package Visibility {#package-visibility}

Visibility is controlled at two levels. **Identifiers** use casing, as in Go: a `PascalCase` type, function, field or method is exported from its package; a `camelCase` one is not.

**Packages** use `internal` directories. A package under a directory named `internal` is importable only from the tree rooted at that directory's parent — Go's rule, applied to GALA import paths:

```
mylib/
  mylib.gala                   package mylib    — public
  internal/
    detail/detail.gala         package detail   — private to example.com/mylib
  sub/
    internal/
      deep/deep.gala           package deep     — private to example.com/mylib/sub
```

`a/b/c/internal/d` is importable only from the tree rooted at `a/b/c`. Only whole path elements count — a directory named `internalize` is an ordinary public package. There is no exemption for the standard library.

This lets a library share code across its own packages without adding it to the API it must keep supporting: consumers call what the parent package exports and cannot reach `internal/detail`, so it can be renamed or deleted freely. A forbidden import is rejected with [GALA-E0041](/docs/errors/gala-e0041/).

Publishing an internal package means moving it out of the `internal` directory, which changes its import path — so it is worth deciding deliberately. For an application, the whole module is already private to you; reach for `internal` where there is a real API boundary to defend, not as a default layout.

See [Dependency Management](/docs/dependency-management/) for managing external packages.

## 14. Embedding Files {#14-embedding-files}

```gala
embed val readme = "README.md"              // string
embed val static EmbeddedFS = "static/*"    // EmbeddedFS

val content = static.ReadString("static/index.html")
```

## 15. Testing {#15-testing}

GALA provides a test framework with 22 assertions, panic recovery, timing, table-driven test support, and benchmarking.

```gala
package main

import . "martianoff/gala/test"

func TestAddition(t T) T {
    val x = 1 + 1
    return Eq(t, x, 2)
}
```

### Table-Driven Tests
```gala
func TestDouble(t T) T {
    return RunCases[int, int](t,
        (sub T, input int, expected int) => Eq(sub, input * 2, expected),
        Case[int, int](Name = "zero", Input = 0, Expected = 0),
        Case[int, int](Name = "positive", Input = 5, Expected = 10),
    )
}
```

### Benchmarking
```gala
func main() {
    RunBenchmarks(
        BenchFunc(Name = "BenchmarkAdd", Func = (b B) => {
            for i := 0; i < b.N; i++ {
                val x = 1 + 1
            }
        }),
    )
}
```

## 16. Best Practices {#16-best-practices}

- **Prefer `val` over `var`** - Use mutable variables only when necessary
- **All parameters, lambda parameters and receivers are immutable** - Declare a parameter or lambda parameter `var` only when its body reassigns it; a receiver is never rebound, so copy it into a local `var` instead (see [Parameters](#parameters))
- **Use `Copy()` for updates** - `person.Copy(age = 31)`
- **Prefer `match` over if-else chains** for type/value dispatch
- **Use extractors** - `case Some(x) =>` not `if opt.IsDefined() { x := opt.Get() }`
- **Omit type parameters when inferrable** - `Some(42)` not `Some[int](42)`
- **Prefer `s"..."` over `fmt.Sprintf`** - `s"Hello $name"` not `fmt.Sprintf("Hello %s", name)`
- **Prefer GALA collections over Go slices** - Use `Array` or `List` from `collection_immutable`
- **Use `Option[T]`** for nullable values, **`Try[T]`** for operations that may fail
- **Use an opaque type, not an alias, for a type of its own** - `type UserID int64` is just `int64`; `opaque type UserID int64` is distinct, takes methods, and still encodes as the bare value (see [Alias, opaque type or private-field struct](#alias-opaque-type-or-private-field-struct))
- **Document exported declarations** with a `//` run directly above them - hover and `gala doc` show it, and it is carried into the generated Go, so `go doc`, gopls, pkg.go.dev and annotation tools such as `swag` read it too. Only declaration docs are carried: comments inside function bodies, trailing comments and `//go:`, `//line` and `// +build` directives are not

## 17. Dependency Management {#17-dependency-management}

GALA provides a module system similar to Go modules. See [Dependency Management](/docs/dependency-management/) for the full guide.

```bash
gala mod init github.com/user/project
gala mod add github.com/example/utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go
gala mod tidy
```

## 18. Further Reading {#18-further-reading}

- [Code Examples](/docs/examples/) - More examples of GALA code
- [Streams](/docs/streams/) - Lazy, potentially infinite sequences
- [Strings](/docs/strings/) - Free functions over `string`, `Str` wrapper, and `StringBuilder`
- [Time Utilities](/docs/time-utils/) - Duration and Instant types
- [Immutable Collections](/docs/immutable-collections/) - Array, List, HashMap, HashSet, TreeSet, TreeMap
- [Mutable Collections](/docs/mutable-collections/) - Mutable collection types
- [Why GALA?](/docs/why-gala/) - Features, trade-offs, and honest assessment
- [Dependency Management](/docs/dependency-management/) - Module system and package management

## 19. IDE Support {#19-ide-support}

### IntelliJ IDEA
A basic IntelliJ IDEA plugin is available in `ide/intellij`.

1. Build: `bazel build //ide/intellij:plugin`
2. Install: Settings > Plugins > Install Plugin from Disk...

The plugin provides syntax highlighting, file type recognition (`.gala`), and basic code structure support.

---

> **Full specification:** This page contains a summary of the GALA language specification with all major features documented. For the complete, unabridged specification with every detail and edge case, see the [GALA.MD source file on GitHub](https://github.com/martianoff/gala/blob/master/docs/GALA.MD).

{% endraw %}
