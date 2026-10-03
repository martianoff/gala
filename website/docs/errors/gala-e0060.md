---
layout: default
title: "GALA-E0060 — val _ = Binds Nothing"
description: "\"`val _ = ...` binds nothing\" — GALA-E0060 fires when a val or var declaration binds its value to _ alone. Write the expression as a bare statement, wrap an error in FromError inside a lambda, or bind a real name."
keywords: "gala-e0060, gala val underscore, gala val _, gala var _, gala discard value, gala blank identifier, gala bare statement, gala FromError"
permalink: /docs/errors/gala-e0060/
last_modified_at: 2026-10-02
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0060</p>

# GALA-E0060 — `val _ =` binds nothing

**When it fires.** A declaration has `_` as its only name and a value:
`val _ = expr`, `var _ = expr`, the same with a type (`val _ T = expr`), or
`_ := expr`. `_` binds nothing, so the declaration only evaluates `expr` —
which is what the expression does on its own, written as a statement.

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
  = hint: write the expression as a bare statement; if the value matters, bind it to a name and use it
```

Inside a lambda the hint also names `FromError`, because there a call that
returns only an `error` cannot be a bare statement:

```gala
package main

import (
    "os"
    . "martianoff/gala/collection_immutable"
)

func main() {
    ArrayOf("a.tmp", "b.tmp").ForEach((p) => {
        val _ = os.Remove(p)
    })
}
```

```
error[GALA-E0060]: `val _ = ...` binds nothing
  --> main.gala:10:9
   |
10 |         val _ = os.Remove(p)
   |         ^^^^^ write the expression as a bare statement
   |
   = hint: write the expression as a bare statement; for a call that returns only an `error`, write `FromError(call())`; if the value matters, bind it to a name and use it
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

In a lambda with no result, a call that returns only an `error` cannot be a
bare statement — GALA refuses to drop the error silently there. Wrap it in
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

- among several names — `val n, _ = strconv.Atoi(s)`, `n, _ := strconv.Atoi(s)`,
  `val (_, b) = pair`, `var (_, d) = pair`;
- as a lambda parameter (`(_ int) => 0`), a match pattern (`case _ =>`) or a
  `for` range variable (`for _, x := range xs`);
- in `var _ T` with no value.

**Rationale.** `val _ =` reads as if something were kept, but nothing is.
Writing the expression on its own says exactly what happens: it runs for its
effect and its value is dropped. The one place a bare statement is refused —
an `error` in a lambda with no result — is refused on purpose, and `val _ =`
(with or without a type) was a way around that check; `FromError` keeps the
error visible instead.
