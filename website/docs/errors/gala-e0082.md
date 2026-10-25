---
layout: default
title: "GALA-E0082 — Wrong Number of Arguments"
description: "\"too many arguments in call\" or \"missing required argument\" — GALA-E0082 fires when a GALA function, method or struct constructor is called with more arguments than it takes, or without one that has no default."
keywords: "gala-e0082, gala too many arguments, gala missing required argument, gala wrong number of arguments, gala arity, gala default parameters, gala struct constructor"
permalink: /docs/errors/gala-e0082/
last_modified_at: 2026-10-24
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0082</p>

# GALA-E0082 — Wrong number of arguments

**When it fires.** A call of a GALA function, method or struct constructor
passes a different number of arguments than the callee accepts:

- more positional arguments than the callee has parameters (or a struct has
  fields);
- fewer than it requires: a parameter left out declares no default.

The count respects everything a GALA signature can say: a parameter with a
default may be left out, a variadic parameter (`xs ...int`) takes any number of
arguments, and named arguments fill the parameters they name.

**Minimal repro.**

```gala
package main

func area(width int, height int) int = width * height

func main() {
    Println(area(3))
}
```

**Error output.**

```
error[GALA-E0082]: missing required argument "height" (parameter 2) in call to area: expected 2, got 1
  --> main.gala:6:18
  |
6 |     Println(area(3))
  |                  ^ pass "height", or declare a default for it
  |
  = hint: pass "height", or declare a default for it; area takes (width, height)
```

The same check covers a call of another package's function, standard library
included: `fs.WriteFileString("out.txt", "x")` leaves out the `mode` parameter
of `WriteFileString(path string, contents string, mode int)`.

A struct constructed with more arguments than it has fields is reported the
same way, naming the fields:

```gala
package main

struct Decision(Id int, State int, Rule int)

func main() {
    Println(Decision(1, 2, 3, 4))
}
```

```
error[GALA-E0082]: too many arguments in call to Decision: expected at most 3, got 4
  --> main.gala:6:22
  |
6 |     Println(Decision(1, 2, 3, 4))
  |                      ^ remove 1 extra argument
  |
  = hint: remove 1 extra argument; Decision has 3 fields: Id, State, Rule
```

**Fix.** Pass one argument per parameter the hint lists; a parameter marked
`= ...` there has a default and may be left out. To make a parameter optional
at every call site, declare a default for it:

```gala
package main

func area(width int, height int = 1) int = width * height

func main() {
    Println(area(3, 4)) // 12
    Println(area(3))    // 3
}
```

**Rationale.** Before this check, a call with the wrong argument count was
emitted as written and reported by `go build` against the generated code —
`not enough arguments in call to fs.WriteFileString have (string, string) want
(string, string, int)` — in Go's vocabulary rather than the declaration's, and
a struct called with one argument too many was reported as
[GALA-E0043](/docs/errors/gala-e0043/), as if the type had no constructor at all.

**Where it stands down.** A function or method declared in Go keeps Go's own
check, and so does a call of a function-typed value. A spread argument
(`f(xs...)`) fills an unknown number of parameters and is not counted.

**Scope.** A shorthand struct field left out of a construction is
[GALA-E0045](/docs/errors/gala-e0045/), which names every missing field at once.
