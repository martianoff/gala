# GALA-E0093 — Sealed type method replaces a generated one with another signature

**When it fires.** A sealed type gets two kinds of methods from its
declaration: `String() string`, which prints a value as its case
(`Circle(2)`), and a case predicate `is<Case>() bool` per case (`isCircle()`).
A method you declare on the sealed type with one of those names replaces the
generated one, so it must have the same signature. One with parameters or
another result fires this error.

**Minimal repro.**

```gala
package main

sealed type Expr {
    case Num(n int)
    case Neg(e Expr)
}

func (e Expr) String() int = 1

func main() {
    Println(Num(1))
}
```

**Error output.**

```
error[GALA-E0093]: method Expr.String must have the signature `String() string`: it replaces the String method generated for sealed type Expr
  --> main.gala:8:15
  |
8 | func (e Expr) String() int = 1
  |               ^^^^^^ give it the signature String() string, or rename it
  |
  = hint: give it the signature String() string, or rename it
```

**Fix.** Give the method the generated one's signature, so it replaces it:

```gala
package main

sealed type Expr {
    case Num(n int)
    case Neg(e Expr)
}

func (e Expr) String() string = e match {
    case Num(n) => s"$n"
    case Neg(x) => s"-${x.String()}"
}

func main() {
    Println(Neg(Num(3)))
}
```

Or keep its signature and give it another name, such as `Render`.

**Rationale.** Printing a value and string interpolation call `String()` and
expect a string, and `is<Case>()` is how GALA code tests a case. A method of
the same name but another signature would leave the type without them, and
used to fail in `go build` ("method already declared").
