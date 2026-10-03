# GALA-E0060 — `val _ =` binds nothing

**When it fires.** A `val` or `var` declaration has `_` as its only name and an
initializer: `val _ = expr` or `var _ = expr`. `_` binds nothing, so the
declaration only evaluates `expr` — which is what the expression does on its
own, written as a statement.

**Minimal repro.**

```gala
package main

func save(name string) bool {
    Println(s"saved $name")
    return true
}

func main() {
    val _ = save("report")
}
```

**Error output.**

```
error[GALA-E0060]: `val _ = ...` binds nothing
  --> main.gala:9:5
  |
9 |     val _ = save("report")
  |     ^^^^^ write the expression as a bare statement
  |
  = hint: write the expression as a bare statement; for an `error`-returning call inside a lambda with no result, write `FromError(call())`; if the value matters, bind it to a name and use it
```

At package level there are no statements, so the hint points at `func init()`:

```gala
package main

func register(name string) bool {
    Println(s"registered $name")
    return true
}

var _ = register("report")

func main() {}
```

```
error[GALA-E0060]: `var _ = ...` binds nothing
  --> main.gala:8:1
  |
8 | var _ = register("report")
  | ^^^^^ to run it for its effect, call it from `func init()`
  |
  = hint: to run it for its effect, call it from `func init()`; if the value matters, bind it to a name and use it
```

**Fix.** Write the expression as a statement:

```gala
package main

func save(name string) bool {
    Println(s"saved $name")
    return true
}

func main() {
    save("report")
}
```

If the value matters, give it a name and use it:

```gala
package main

func save(name string) bool {
    Println(s"saved $name")
    return true
}

func main() {
    val saved = save("report")
    Println(s"saved: $saved")
}
```

A call that returns only an `error` cannot be a bare statement inside a lambda
with no result — GALA refuses to drop the error silently there. Wrap it in
`FromError`, which turns the error into a `Try[Void]` you can inspect or
ignore:

```gala
package main

import (
    "os"
    . "martianoff/gala/collection_immutable"
)

func main() {
    ArrayOf("a.tmp", "b.tmp").ForEach((p) => {
        FromError(os.Remove(p))
    })
}
```

At package level, move the call into `func init()`:

```gala
package main

func register(name string) bool {
    Println(s"registered $name")
    return true
}

func init() {
    register("report")
}

func main() {}
```

**What still works.** `_` keeps every other meaning it has:

- among several names — `val n, _ = strconv.Atoi(s)`, `val (_, b) = pair`,
  `var (_, d) = pair`;
- as a lambda parameter (`(_ int) => 0`), a match pattern (`case _ =>`) or a
  `for` range variable (`for _, x := range xs`);
- with a declared type: `val _ Shape = Circle(1.0)` checks at compile time that
  a `Circle` is a `Shape`, which the bare expression does not.

**Rationale.** `val _ =` reads as if something were kept, but nothing is.
Writing the expression on its own says exactly what happens: it runs for its
effect and its value is dropped. The one case where a bare statement is refused
— an `error` in a lambda with no result — is refused on purpose, and `val _ =`
was a way around that check; `FromError` keeps the error visible instead.
