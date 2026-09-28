# GALA-E0054 — value called as a function

**When it fires.** A value was called with `(...)`, but its type is not a
function type:

```gala
struct Color(N int)

val Red = Color(1)

Println(Red())     // Red is a val of type Color
Println(Red.N())   // N is a field of type int
```

This applies to a `val` or `var`, a parameter, and a struct field, whether it
is declared in this package or in an imported one (qualified, `colors.Green()`,
or dot-imported, `Green()`). A `val` is read by its name alone. The parentheses
of a call belong only to a function.

**Minimal repro.**

```gala
package main

struct Color(N int)

val Red = Color(1)

func main() {
    Println(Red())
}
```

**Error output.**

```
error[GALA-E0054]: Red is a val of type Color, not a function
  --> main.gala:8:13
  |
8 |     Println(Red())
  |             ^^^ remove the parentheses to read the val: `Red`
  |
  = hint: remove the parentheses to read the val: `Red`; only a value of function type, or of a type with an Apply method, can be called
```

For a field, the message names the struct and the field:

```
error[GALA-E0054]: Color.N is a field of type int, not a function
```

**Fix.** Drop the parentheses:

```gala
package main

struct Color(N int)

val Red = Color(1)

func main() {
    Println(Red)
    Println(Red.N)
}
```

**What still works.** Calling a value is fine when its type can be called:

```gala
val inc = (x int) => x + 1
Println(inc(1))                     // a val holding a lambda

type Op func(int) int
val twice Op = (x int) => x * 2
Println(twice(3))                   // a named function type

struct Adder(K int)
func (a Adder) Apply(x int) int = a.K + x
val addTwo = Adder(2)
Println(addTwo(1))                  // a type with an Apply method

struct Handler(OnEvent func(string) string)
val h = Handler((s string) => s)
Println(h.OnEvent("x"))             // a field of function type
```

**Rationale.** The call used to be emitted as written and left to `go build`,
which rejected it with a message about the generated code:

```
invalid operation: cannot call Red.Get() (value of struct type Color): Color is not a function
```

`Red.Get()` is the unwrap GALA inserts to read a `val`; nothing in the source
spells it. The line number was right, but the sentence described Go, not GALA.
The mistake it reports is usually a stray pair of parentheses: a value read the
way a zero-argument accessor is called, or a `val` remembered as the function
that used to build it.

**Scope.** The check fires only when the value's type is known not to be
callable: a predeclared type such as `int`, `string` or `bool`, or a GALA struct
or sealed type that has no `Apply` method. A value of an interface type, a type
parameter, a Go type or any type GALA cannot see into is left to Go. It does not
cover:

- a type name called as a constructor, such as `Array(1, 2, 3)` — [GALA-E0043](GALA-E0043.md);
- a method that the type does not declare — [GALA-E0044](GALA-E0044.md);
- a name that resolves to nothing — [GALA-E0023](GALA-E0023.md).
