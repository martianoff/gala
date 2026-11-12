# GALA-E0098 — Field default reads a field

**When it fires.** The default value of a shorthand struct's field or a
sealed case's field reads a field of the same struct or case: another field,
or the field itself, directly or anywhere in the expression. A field default
is evaluated with no field of its type in scope: a name in it that is declared
outside the type (a package-level val or constant, a type, an import) means
that, even where a field is spelled the same, and one that is not, but is
spelled like a field, is this error. A function's default, by contrast, may read the parameters
before it (see [GALA-E0094](GALA-E0094.md)).

**Minimal repro.**

```gala
package main

struct Rect(W int, H int = W)

func main() {
    Println(Rect(2).H)
}
```

**Error output.**

```
error[GALA-E0098]: the default value of field H reads field W of Rect
  --> main.gala:3:28
  |
3 | struct Rect(W int, H int = W)
  |                            ^ a field default may not read a field of its type
  |
  = hint: a field default may not read a field of its type; give H a default that reads no field, or compute it in a constructor function instead
```

**Fix.** Compute the field in a constructor function:

```gala
package main

struct Rect(W int, H int)

func square(side int) Rect = Rect(side, side)

func main() {
    Println(square(2).H)
}
```

**Rationale.** Go code builds the struct through its generated `New<T>`
constructor, which sets each field to its default before it applies the
caller's options, so a default that read another field could not see the value
an option gives that field. Scala rejects the same declaration: `case class Rect(w: Int, h: Int = w)` does not compile.
